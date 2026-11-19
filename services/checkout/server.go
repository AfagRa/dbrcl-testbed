package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"dbrcl-testbed/services/internal/jlog"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Config for one checkout instance.
type Config struct {
	Version        string // stable | canary
	Pod            string
	PaymentURL     string // base URL, e.g. http://payment.dbrcl.svc.cluster.local:8080
	PaymentTimeout time.Duration
	MemLimit       int64
}

// Server is the checkout HTTP service.
type Server struct {
	cfg    Config
	eng    *Engine
	client *http.Client
	reg    *prometheus.Registry

	reqs     *prometheus.CounterVec
	dur      prometheus.Histogram
	payErrs  prometheus.Counter
	faultAct *prometheus.GaugeVec
}

// Latency buckets chosen to resolve P99 between 5 ms and 5 s.
var durationBuckets = []float64{
	.005, .01, .02, .03, .05, .075, .1, .15, .2, .25, .3, .4, .5, .75, 1, 1.5, 2, 3, 5,
}

// NewServer builds the service and its metrics registry.
func NewServer(cfg Config, l *jlog.Logger) *Server {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	labels := prometheus.Labels{"version": cfg.Version, "pod": cfg.Pod}
	// Keep-alive pool sized for the load (default is 2 idle conns per host).
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConns, tr.MaxIdleConnsPerHost = 512, 256
	s := &Server{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.PaymentTimeout, Transport: tr},
		reg:    reg,
		reqs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "checkout_requests_total", Help: "Checkout requests by status code.", ConstLabels: labels,
		}, []string{"code"}),
		dur: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "checkout_request_duration_seconds", Help: "Checkout request latency.", ConstLabels: labels,
			Buckets: durationBuckets,
		}),
		payErrs: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "checkout_payment_errors_total", Help: "Failed payment calls.", ConstLabels: labels,
		}),
		faultAct: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "checkout_fault_active", Help: "1 while the named fault is active.", ConstLabels: labels,
		}, []string{"fault"}),
	}
	reg.MustRegister(s.reqs, s.dur, s.payErrs, s.faultAct)
	for _, f := range knownFaults {
		s.faultAct.WithLabelValues(f).Set(0)
	}
	s.eng = NewEngine(cfg.Pod, cfg.MemLimit, l, func(name string, on bool) {
		v := 0.0
		if on {
			v = 1
		}
		s.faultAct.WithLabelValues(name).Set(v)
	})
	return s
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /checkout", s.checkout)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ready")) })
	mux.Handle("GET /metrics", promhttp.HandlerFor(s.reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("GET /admin/fault", s.getFault)
	mux.HandleFunc("POST /admin/fault", s.setFault)
	return mux
}

type cartItem struct {
	SKU        string `json:"sku"`
	Qty        int64  `json:"qty"`
	PriceCents int64  `json:"price_cents"`
}

type cart struct {
	Items []cartItem `json:"items"`
}

func (c cart) total() (int64, error) {
	if len(c.Items) == 0 || len(c.Items) > 50 {
		return 0, errors.New("cart must have 1..50 items")
	}
	var t int64
	for _, it := range c.Items {
		if it.SKU == "" || it.Qty < 1 || it.Qty > 100 || it.PriceCents < 0 || it.PriceCents > 1_000_000 {
			return 0, fmt.Errorf("invalid item %q", it.SKU)
		}
		t += it.Qty * it.PriceCents
	}
	return t, nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func newOrderID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) checkout(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	code := http.StatusOK
	defer func() {
		s.reqs.WithLabelValues(strconv.Itoa(code)).Inc()
		s.dur.Observe(time.Since(start).Seconds())
	}()
	reply := func(c int, v any) { code = c; writeJSON(w, c, v) }

	fx := s.eng.OnRequest()
	if fx.ExtraLatency > 0 {
		select {
		case <-time.After(fx.ExtraLatency):
		case <-r.Context().Done():
			code = 499
			return
		}
	}
	if fx.ForceStatus != 0 {
		reply(fx.ForceStatus, map[string]any{"error": "injected failure", "version": s.cfg.Version})
		return
	}

	var c cart
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&c); err != nil {
		reply(http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	total, err := c.total()
	if err != nil {
		reply(http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	orderID := newOrderID()
	if err := s.pay(r.Context(), orderID, total); err != nil {
		s.payErrs.Inc()
		reply(http.StatusBadGateway, map[string]any{"error": "payment failed", "version": s.cfg.Version})
		return
	}
	reply(http.StatusOK, map[string]any{"order_id": orderID, "total_cents": total, "version": s.cfg.Version})
}

func (s *Server) pay(ctx context.Context, orderID string, amount int64) error {
	if fail, timeout := s.eng.OnPayment(); fail {
		if timeout { // behave like a dependency that never answers
			select {
			case <-time.After(s.cfg.PaymentTimeout):
			case <-ctx.Done():
			}
		}
		return errInjected
	}
	body, _ := json.Marshal(map[string]any{"order_id": orderID, "amount_cents": amount})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.PaymentURL+"/pay", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	// Drain before closing: the transport only reuses a connection whose body
	// was read to EOF. Without this every checkout re-dialed (and re-resolved) payment.
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("payment status %d", resp.StatusCode)
	}
	return nil
}

func (s *Server) getFault(w http.ResponseWriter, _ *http.Request) {
	cfg, since := s.eng.Current()
	out := map[string]any{"version": s.cfg.Version, "pod": s.cfg.Pod, "fault": "none"}
	if cfg.Name != "" {
		out["fault"] = cfg.Name
		out["params"] = cfg.effectiveParams()
		out["since"] = since.UTC().Format(jlog.TSFormat)
	}
	if n := s.eng.RetainedBytes(); n > 0 {
		out["leak_retained_bytes"] = n
	}
	writeJSON(w, http.StatusOK, out)
}

type faultReq struct {
	Fault  string         `json:"fault"`
	Params map[string]any `json:"params"`
}

func (s *Server) setFault(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Version != "canary" {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "fault injection is only allowed on the canary"})
		return
	}
	var req faultReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil || req.Fault == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "body must be {\"fault\": ..., \"params\": {...}}"})
		return
	}
	if req.Fault == "none" {
		s.eng.Deactivate()
	} else if err := s.eng.Activate(req.Fault, req.Params); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	s.getFault(w, r)
}
