// checkout: one codebase for stable and canary, selected by env VERSION.
// The fault engine (POST /admin/fault) only works when VERSION=canary.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"dbrcl-testbed/services/internal/jlog"
)

const defaultMemLimit = 256 << 20

// memoryLimit reads the cgroup limit (v2, then v1), then MEMORY_LIMIT_BYTES.
func memoryLimit() int64 {
	if v, err := strconv.ParseInt(os.Getenv("MEMORY_LIMIT_BYTES"), 10, 64); err == nil && v > 0 {
		return v
	}
	for _, f := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil && v > 0 && v < 1<<50 {
			return v
		}
	}
	return defaultMemLimit
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	version := env("VERSION", "stable")
	if version != "stable" && version != "canary" {
		log.Fatalf("VERSION must be stable or canary, got %q", version)
	}
	pod := os.Getenv("POD_NAME")
	if pod == "" {
		pod, _ = os.Hostname()
	}
	timeoutMs, _ := strconv.Atoi(env("PAYMENT_TIMEOUT_MS", "1000"))
	cfg := Config{
		Version:        version,
		Pod:            pod,
		PaymentURL:     env("PAYMENT_URL", "http://payment.dbrcl.svc.cluster.local:8080"),
		PaymentTimeout: time.Duration(timeoutMs) * time.Millisecond,
		MemLimit:       memoryLimit(),
	}
	l := jlog.New(nil)
	s := NewServer(cfg, l)
	addr := ":" + env("PORT", "8080")
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	l.Log(map[string]any{"event": "start", "service": "checkout", "version": version, "pod": pod,
		"addr": addr, "mem_limit_bytes": cfg.MemLimit, "payment_url": cfg.PaymentURL})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	s.eng.Deactivate() // log fault_off on shutdown so every fault_on has a matching end
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(sctx)
}
