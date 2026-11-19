// router: reverse proxy in front of checkout-stable and checkout-canary with
// deterministic nested cohorts: canary iff FNV-1a(uid) mod 10^4 < round(10^4*w).
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"dbrcl-testbed/services/internal/jlog"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	headerUID       = "X-User-ID"
	headerServedBy  = "X-Served-By"
	headerGenerated = "X-User-ID-Generated" // "true" when the router invented the uid
)

// Config for the router.
type Config struct {
	StableURL, CanaryURL string
	SLOLatency           time.Duration
	InitialWeight        float64
}

// Router routes requests and owns the weight.
type Router struct {
	cfg    Config
	log    *jlog.Logger
	ws     weightState
	stable *httputil.ReverseProxy
	canary *httputil.ReverseProxy
	reg    *prometheus.Registry

	reqs   *prometheus.CounterVec
	dur    *prometheus.HistogramVec
	weight prometheus.Gauge
	slo    *prometheus.CounterVec
}

var durationBuckets = []float64{
	.005, .01, .02, .03, .05, .075, .1, .15, .2, .25, .3, .4, .5, .75, 1, 1.5, 2, 3, 5,
}

func newProxy(raw string) (*httputil.ReverseProxy, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, errors.New("invalid upstream URL " + raw)
	}
	p := httputil.NewSingleHostReverseProxy(u)
	// The default transport keeps only 2 idle connections per host; above a few
	// concurrent requests every extra request would open a new TCP connection.
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns, t.MaxIdleConnsPerHost = 512, 256
	p.Transport = t
	p.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		w.WriteHeader(http.StatusBadGateway)
	}
	return p, nil
}

// NewRouter builds the router with weight cfg.InitialWeight (stage 0).
func NewRouter(cfg Config, l *jlog.Logger) (*Router, error) {
	st, err := newProxy(cfg.StableURL)
	if err != nil {
		return nil, err
	}
	ca, err := newProxy(cfg.CanaryURL)
	if err != nil {
		return nil, err
	}
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	r := &Router{
		cfg: cfg, log: l, stable: st, canary: ca, reg: reg,
		reqs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "router_requests_total", Help: "Routed requests by version and status code.",
		}, []string{"version", "code"}),
		dur: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "router_request_duration_seconds", Help: "End-to-end latency through the router.",
			Buckets: durationBuckets,
		}, []string{"version"}),
		weight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "router_canary_weight", Help: "Current canary weight w.",
		}),
		slo: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "router_slo_violations_total", Help: "Requests with status >= 500 or latency above the SLO.",
		}, []string{"version"}),
	}
	reg.MustRegister(r.reqs, r.dur, r.weight, r.slo)
	for _, v := range []string{"stable", "canary"} {
		r.slo.WithLabelValues(v)
		r.dur.WithLabelValues(v)
	}
	if cfg.InitialWeight < 0 || cfg.InitialWeight > 1 {
		return nil, errors.New("initial weight out of [0,1]")
	}
	r.ws.set(cfg.InitialWeight, 0, "initial")
	r.weight.Set(cfg.InitialWeight)
	l.Log(map[string]any{"event": "weight_change", "old": nil, "new": cfg.InitialWeight, "stage": 0, "reason": "initial"})
	return r, nil
}

// Handler returns the router's routes: admin and metrics, everything else proxied.
func (r *Router) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/weight", r.getWeight)
	mux.HandleFunc("PUT /admin/weight", r.putWeight)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.Handle("GET /metrics", promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/", r.route)
	return mux
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (s *statusWriter) WriteHeader(c int) {
	if s.code == 0 {
		s.code = c
	}
	s.ResponseWriter.WriteHeader(c)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.code == 0 {
		s.code = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func randomUID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "anon-" + hex.EncodeToString(b)
}

func (r *Router) route(w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	uid := req.Header.Get(headerUID)
	generated := uid == ""
	if generated {
		uid = randomUID()
		req.Header.Set(headerUID, uid)
		w.Header().Set(headerGenerated, "true")
		w.Header().Set(headerUID, uid)
	}
	bucket := Bucket(uid)
	weight, threshold, stage, _, _ := r.ws.get()
	version, proxy := "stable", r.stable
	if ToCanary(bucket, threshold) {
		version, proxy = "canary", r.canary
	}
	w.Header().Set(headerServedBy, version)

	sw := &statusWriter{ResponseWriter: w}
	proxy.ServeHTTP(sw, req)
	if sw.code == 0 {
		sw.code = http.StatusOK
	}
	lat := time.Since(start)
	violation := sw.code >= 500 || lat > r.cfg.SLOLatency

	r.reqs.WithLabelValues(version, strconv.Itoa(sw.code)).Inc()
	r.dur.WithLabelValues(version).Observe(lat.Seconds())
	if violation {
		r.slo.WithLabelValues(version).Inc()
	}
	// ts is the routing decision time (request arrival), so a request is
	// attributed to the weight that was in force when it was routed.
	r.log.Log(map[string]any{
		"ts": start.UTC().Format(jlog.TSFormat), "event": "request",
		"uid": uid, "uid_generated": generated, "bucket": bucket,
		"weight": weight, "stage": stage, "version": version, "path": req.URL.Path,
		"status": sw.code, "latency_ms": float64(lat.Microseconds()) / 1000, "slo_violation": violation,
	})
}

type weightBody struct {
	Weight *float64 `json:"weight"`
	Stage  int      `json:"stage"`
	Reason string   `json:"reason"`
}

func (r *Router) getWeight(w http.ResponseWriter, _ *http.Request) {
	weight, threshold, stage, reason, since := r.ws.get()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"weight": weight, "threshold": threshold, "buckets": Buckets, "stage": stage,
		"reason": reason, "since": since.UTC().Format(jlog.TSFormat),
	})
}

func (r *Router) putWeight(w http.ResponseWriter, req *http.Request) {
	var b weightBody
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<14)).Decode(&b); err != nil ||
		b.Weight == nil || *b.Weight < 0 || *b.Weight > 1 || b.Stage < 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"body must be {\"weight\": 0..1, \"stage\": >=0, \"reason\": \"...\"}"}` + "\n"))
		return
	}
	old, _ := r.ws.set(*b.Weight, b.Stage, b.Reason)
	r.weight.Set(*b.Weight)
	r.log.Log(map[string]any{"event": "weight_change", "old": old, "new": *b.Weight, "stage": b.Stage, "reason": b.Reason})
	r.getWeight(w, req)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	sloMs, err := strconv.Atoi(env("SLO_LATENCY_MS", "300"))
	if err != nil {
		log.Fatal("SLO_LATENCY_MS: ", err)
	}
	w0, err := strconv.ParseFloat(env("INITIAL_WEIGHT", "0"), 64)
	if err != nil {
		log.Fatal("INITIAL_WEIGHT: ", err)
	}
	l := jlog.New(nil)
	r, err := NewRouter(Config{
		StableURL:     env("STABLE_URL", "http://checkout-stable.dbrcl.svc.cluster.local:8080"),
		CanaryURL:     env("CANARY_URL", "http://checkout-canary.dbrcl.svc.cluster.local:8080"),
		SLOLatency:    time.Duration(sloMs) * time.Millisecond,
		InitialWeight: w0,
	}, l)
	if err != nil {
		log.Fatal(err)
	}
	addr := ":" + env("PORT", "8080")
	srv := &http.Server{Addr: addr, Handler: r.Handler(), ReadHeaderTimeout: 5 * time.Second}
	l.Log(map[string]any{"event": "start", "service": "router", "addr": addr,
		"stable": r.cfg.StableURL, "canary": r.cfg.CanaryURL, "slo_latency_ms": sloMs})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(sctx)
}
