// payment is a mock dependency: POST /pay returns 200 after a configurable
// latency (default 20 ms +/- 5 ms uniform jitter).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math/rand/v2"
	"net/http"
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

type server struct {
	latency, jitter time.Duration
	reqs            *prometheus.CounterVec
	dur             prometheus.Histogram
	reg             *prometheus.Registry
}

func newServer(latency, jitter time.Duration, pod string) *server {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	labels := prometheus.Labels{"pod": pod}
	s := &server{
		latency: latency, jitter: jitter, reg: reg,
		reqs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "payment_requests_total", Help: "Payment requests by status code.", ConstLabels: labels,
		}, []string{"code"}),
		dur: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "payment_request_duration_seconds", Help: "Payment request latency.", ConstLabels: labels,
			Buckets: []float64{.005, .01, .015, .02, .025, .03, .04, .05, .075, .1, .25, .5, 1},
		}),
	}
	reg.MustRegister(s.reqs, s.dur)
	return s
}

type payReq struct {
	OrderID     string `json:"order_id"`
	AmountCents int64  `json:"amount_cents"`
}

func (s *server) pay(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	code := http.StatusOK
	defer func() {
		s.reqs.WithLabelValues(strconv.Itoa(code)).Inc()
		s.dur.Observe(time.Since(start).Seconds())
	}()
	var req payReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil || req.AmountCents < 0 {
		code = http.StatusBadRequest
		http.Error(w, `{"error":"invalid payment"}`, code)
		return
	}
	d := s.latency
	if s.jitter > 0 {
		d += time.Duration(rand.Int64N(int64(2*s.jitter)+1)) - s.jitter
	}
	select {
	case <-time.After(d):
	case <-r.Context().Done():
		code = 499
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"status": "ok", "order_id": req.OrderID})
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /pay", s.pay)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.Handle("GET /metrics", promhttp.HandlerFor(s.reg, promhttp.HandlerOpts{}))
	return mux
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

func main() {
	pod := os.Getenv("POD_NAME")
	if pod == "" {
		pod, _ = os.Hostname()
	}
	s := newServer(time.Duration(envInt("PAYMENT_LATENCY_MS", 20))*time.Millisecond,
		time.Duration(envInt("PAYMENT_JITTER_MS", 5))*time.Millisecond, pod)
	addr := ":" + strconv.Itoa(envInt("PORT", 8080))
	srv := &http.Server{Addr: addr, Handler: s.handler(), ReadHeaderTimeout: 5 * time.Second}
	jlog.New(nil).Log(map[string]any{"event": "start", "service": "payment", "addr": addr, "pod": pod})

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
