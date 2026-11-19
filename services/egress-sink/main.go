// egress-sink: in-cluster TCP sink for the unexpected_egress scenario. It
// accepts connections on SINK_PORTS, reads a few bytes, logs one JSON line per
// connection with the peer address, and exposes sink_connections_total.
// This is the sink-side view: the canary cannot spoof what the sink observes.
package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"dbrcl-testbed/services/internal/jlog"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type sink struct {
	log   *jlog.Logger
	conns *prometheus.CounterVec
	reg   *prometheus.Registry
}

func newSink(l *jlog.Logger) *sink {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	s := &sink{log: l, reg: reg, conns: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "sink_connections_total", Help: "Accepted TCP connections by peer IP and sink port.",
	}, []string{"source_ip", "port"})}
	reg.MustRegister(s.conns)
	return s
}

// serve accepts on ln until it is closed.
func (s *sink) serve(ln net.Listener, port int, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go s.handle(c, port)
	}
}

func (s *sink) handle(c net.Conn, port int) {
	defer c.Close()
	ts := jlog.Now() // accept time
	ip, sport := "", 0
	if a, ok := c.RemoteAddr().(*net.TCPAddr); ok {
		ip, sport = a.IP.String(), a.Port
	}
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _ := io.Copy(io.Discard, io.LimitReader(c, 4096))
	s.conns.WithLabelValues(ip, strconv.Itoa(port)).Inc()
	s.log.Log(map[string]any{"ts": ts, "event": "connection", "source_ip": ip, "source_port": sport,
		"port": port, "bytes": n})
}

func parsePorts(v string) ([]int, error) {
	var out []int
	for _, f := range strings.Split(v, ",") {
		p, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || p < 1 || p > 65535 {
			return nil, errors.New("invalid port " + f)
		}
		out = append(out, p)
	}
	return out, nil
}

func main() {
	ports, err := parsePorts(envOr("SINK_PORTS", "9000"))
	if err != nil {
		log.Fatal(err)
	}
	l := jlog.New(nil)
	s := newSink(l)
	var wg sync.WaitGroup
	var lns []net.Listener
	for _, p := range ports {
		ln, err := net.Listen("tcp", ":"+strconv.Itoa(p))
		if err != nil {
			log.Fatal(err)
		}
		lns = append(lns, ln)
		wg.Add(1)
		go s.serve(ln, p, &wg)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(s.reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	addr := ":" + envOr("METRICS_PORT", "9100")
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	l.Log(map[string]any{"event": "start", "service": "egress-sink", "ports": ports, "metrics_addr": addr})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	for _, ln := range lns {
		ln.Close()
	}
	wg.Wait()
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(sctx)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
