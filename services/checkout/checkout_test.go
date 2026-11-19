package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"dbrcl-testbed/services/internal/jlog"
)

const cartBody = `{"items":[{"sku":"a","qty":2,"price_cents":500},{"sku":"b","qty":1,"price_cents":250}]}`

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type fixture struct {
	srv  *Server
	ts   *httptest.Server
	logs *syncBuf
}

func newFixture(t *testing.T, version string) *fixture {
	t.Helper()
	pay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(pay.Close)
	logs := &syncBuf{}
	s := NewServer(Config{Version: version, Pod: version + "-pod", PaymentURL: pay.URL,
		PaymentTimeout: 50 * time.Millisecond, MemLimit: 64 << 20}, jlog.New(logs))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(func() { s.eng.Deactivate(); ts.Close() })
	return &fixture{srv: s, ts: ts, logs: logs}
}

func (f *fixture) checkout(t *testing.T) (int, time.Duration) {
	t.Helper()
	start := time.Now()
	resp, err := http.Post(f.ts.URL+"/checkout", "application/json", strings.NewReader(cartBody))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode, time.Since(start)
}

func (f *fixture) setFault(t *testing.T, body string) int {
	t.Helper()
	resp, err := http.Post(f.ts.URL+"/admin/fault", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func (f *fixture) metrics(t *testing.T) string {
	resp, err := http.Get(f.ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

var tsRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)

// faultEvents returns the fault_on/fault_off log lines.
func (f *fixture) faultEvents(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(strings.NewReader(f.logs.String()))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("non-JSON log line %q", sc.Text())
		}
		if ev := m["event"]; ev == "fault_on" || ev == "fault_off" {
			if !tsRe.MatchString(m["ts"].(string)) {
				t.Fatalf("bad ts %v", m["ts"])
			}
			out = append(out, m)
		}
	}
	return out
}

func expectOnOff(t *testing.T, f *fixture, fault string) {
	t.Helper()
	ev := f.faultEvents(t)
	if len(ev) != 2 || ev[0]["event"] != "fault_on" || ev[1]["event"] != "fault_off" ||
		ev[0]["fault"] != fault || ev[1]["fault"] != fault || ev[0]["pod"] != "canary-pod" {
		t.Fatalf("want fault_on/fault_off for %s, got %v", fault, ev)
	}
}

func TestHealthyCheckout(t *testing.T) {
	f := newFixture(t, "stable")
	if code, _ := f.checkout(t); code != 200 {
		t.Fatalf("status %d", code)
	}
	resp, _ := http.Post(f.ts.URL+"/checkout", "application/json", strings.NewReader(`{"items":[]}`))
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("empty cart: status %d", resp.StatusCode)
	}
	m := f.metrics(t)
	for _, want := range []string{
		`checkout_requests_total{code="200",pod="stable-pod",version="stable"} 1`,
		`checkout_request_duration_seconds_bucket{pod="stable-pod",version="stable",le="0.005"}`,
		`checkout_fault_active{fault="mem_leak",pod="stable-pod",version="stable"} 0`,
	} {
		if !strings.Contains(m, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}

func TestStableRefusesFault(t *testing.T) {
	f := newFixture(t, "stable")
	if code := f.setFault(t, `{"fault":"error_burst","params":{"p":1}}`); code != 403 {
		t.Fatalf("want 403, got %d", code)
	}
	if code, _ := f.checkout(t); code != 200 {
		t.Fatalf("stable checkout status %d", code)
	}
	if len(f.faultEvents(t)) != 0 {
		t.Fatal("stable must not log fault events")
	}
}

func TestInvalidFaultRequests(t *testing.T) {
	f := newFixture(t, "canary")
	for _, body := range []string{
		`{"fault":"nope"}`, `{"fault":"error_burst","params":{"p":2}}`,
		`{"fault":"dependency_failure","params":{"mode":"weird"}}`,
		`{"fault":"unexpected_egress","params":{"extra_ports":[70000]}}`, `{}`,
	} {
		if code := f.setFault(t, body); code != 400 {
			t.Errorf("%s: want 400, got %d", body, code)
		}
	}
}

func TestErrorBurst(t *testing.T) {
	f := newFixture(t, "canary")
	if code := f.setFault(t, `{"fault":"error_burst","params":{"p":1}}`); code != 200 {
		t.Fatalf("activate: %d", code)
	}
	if !strings.Contains(f.metrics(t), `checkout_fault_active{fault="error_burst",pod="canary-pod",version="canary"} 1`) {
		t.Error("fault gauge not set")
	}
	for i := 0; i < 20; i++ {
		if code, _ := f.checkout(t); code != 500 {
			t.Fatalf("request %d: want 500, got %d", i, code)
		}
	}
	f.setFault(t, `{"fault":"none"}`)
	if code, _ := f.checkout(t); code != 200 {
		t.Fatalf("after off: %d", code)
	}
	expectOnOff(t, f, "error_burst")
}

func TestDependencyFailure(t *testing.T) {
	f := newFixture(t, "canary")
	f.setFault(t, `{"fault":"dependency_failure","params":{"p":1,"mode":"error"}}`)
	if code, _ := f.checkout(t); code != 502 {
		t.Fatalf("error mode: want 502, got %d", code)
	}
	f.setFault(t, `{"fault":"dependency_failure","params":{"p":1,"mode":"timeout"}}`)
	code, el := f.checkout(t)
	if code != 502 || el < 50*time.Millisecond {
		t.Fatalf("timeout mode: want 502 after >=50ms, got %d after %v", code, el)
	}
	if !strings.Contains(f.metrics(t), `checkout_payment_errors_total{pod="canary-pod",version="canary"} 2`) {
		t.Error("payment errors not counted")
	}
	f.setFault(t, `{"fault":"none"}`)
	if code, _ := f.checkout(t); code != 200 {
		t.Fatalf("after off: %d", code)
	}
	// replacing a fault logs fault_off for the old one first
	ev := f.faultEvents(t)
	if len(ev) != 4 || ev[1]["event"] != "fault_off" || ev[2]["event"] != "fault_on" || ev[3]["event"] != "fault_off" {
		t.Fatalf("unexpected events %v", ev)
	}
}

func TestLatencyRegression(t *testing.T) {
	f := newFixture(t, "canary")
	f.setFault(t, `{"fault":"latency_regression","params":{"median_ms":80,"sigma":0}}`)
	if _, el := f.checkout(t); el < 80*time.Millisecond {
		t.Fatalf("on: latency %v < 80ms", el)
	}
	f.setFault(t, `{"fault":"none"}`)
	if _, el := f.checkout(t); el >= 80*time.Millisecond {
		t.Fatalf("off: latency %v still >= 80ms", el)
	}
	expectOnOff(t, f, "latency_regression")
}

func TestUnexpectedEgress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 100)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			b, _ := io.ReadAll(c)
			c.Close()
			got <- string(b)
		}
	}()

	f := newFixture(t, "canary")
	f.srv.eng.sinkHost = "127.0.0.1"
	f.srv.eng.sinkPort = ln.Addr().(*net.TCPAddr).Port

	f.setFault(t, `{"fault":"unexpected_egress","params":{"p":1}}`)
	if code, _ := f.checkout(t); code != 200 {
		t.Fatalf("egress fault must not change the response, got %d", code)
	}
	select {
	case s := <-got:
		if s != "dbrcl-egress-test\n" {
			t.Fatalf("payload %q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no connection to sink")
	}

	f.setFault(t, `{"fault":"none"}`)
	for i := 0; i < 10; i++ {
		f.checkout(t)
	}
	select {
	case <-got:
		t.Fatal("connection after deactivation")
	case <-time.After(300 * time.Millisecond):
	}
	expectOnOff(t, f, "unexpected_egress")
}

func TestMemLeak(t *testing.T) {
	f := newFixture(t, "canary") // MemLimit 64 MiB
	e := f.srv.eng
	e.chunk = 64 << 10
	e.leakTick = 10 * time.Millisecond
	f.setFault(t, `{"fault":"mem_leak","params":{"target_fraction":0.5,"tau_seconds":0.05}}`)
	target := int64(32 << 20)

	time.Sleep(25 * time.Millisecond) // ~0.5 tau: well below target
	early := e.RetainedBytes()
	time.Sleep(400 * time.Millisecond) // ~8 tau: near the asymptote
	late := e.RetainedBytes()
	if early <= 0 || early >= late {
		t.Fatalf("leak not growing: early=%d late=%d", early, late)
	}
	if late < target*9/10 || late > target {
		t.Fatalf("leak %d not converging to target %d", late, target)
	}
	f.setFault(t, `{"fault":"none"}`)
	if e.RetainedBytes() != 0 {
		t.Fatal("leak not released")
	}
	expectOnOff(t, f, "mem_leak")
}

func TestCPUSaturation(t *testing.T) {
	f := newFixture(t, "canary")
	f.setFault(t, `{"fault":"cpu_saturation","params":{"workers":3,"duty":0.5}}`)
	deadline := time.Now().Add(time.Second)
	for f.srv.eng.cpuActive.Load() != 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := f.srv.eng.cpuActive.Load(); n != 3 {
		t.Fatalf("want 3 busy workers, got %d", n)
	}
	f.setFault(t, `{"fault":"none"}`)
	if n := f.srv.eng.cpuActive.Load(); n != 0 {
		t.Fatalf("workers still running: %d", n)
	}
	expectOnOff(t, f, "cpu_saturation")
}
