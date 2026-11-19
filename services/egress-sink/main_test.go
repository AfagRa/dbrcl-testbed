package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"dbrcl-testbed/services/internal/jlog"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func TestSinkLogsAndCounts(t *testing.T) {
	logs := &syncBuf{}
	s := newSink(jlog.New(logs))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go s.serve(ln, 9000, &wg)

	for i := 0; i < 3; i++ {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		c.Write([]byte("dbrcl-egress-test\n"))
		c.Close()
	}
	deadline := time.Now().Add(2 * time.Second)
	for strings.Count(logs.String(), "\n") < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	ln.Close()
	wg.Wait()

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 log lines, got %d: %q", len(lines), logs.String())
	}
	var m map[string]any
	json.Unmarshal([]byte(lines[0]), &m)
	if m["event"] != "connection" || m["source_ip"] != "127.0.0.1" || m["port"] != 9000.0 || m["bytes"] != 18.0 || m["source_port"] == 0.0 {
		t.Fatalf("log line %v", m)
	}

	ts := httptest.NewServer(promhttp.HandlerFor(s.reg, promhttp.HandlerOpts{}))
	defer ts.Close()
	resp, _ := ts.Client().Get(ts.URL)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `sink_connections_total{port="9000",source_ip="127.0.0.1"} 3`) {
		t.Fatalf("metric missing:\n%s", body)
	}
}

func TestParsePorts(t *testing.T) {
	if p, err := parsePorts("9000, 9001"); err != nil || len(p) != 2 || p[1] != 9001 {
		t.Fatal(p, err)
	}
	if _, err := parsePorts("9000,x"); err == nil {
		t.Fatal("want error")
	}
}
