package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"dbrcl-testbed/services/internal/jlog"
)

var stages = []float64{0.01, 0.05, 0.25, 1.0}

func randomUIDs(n int, seed uint64) []string {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("uid-%016x", r.Uint64())
	}
	return out
}

// Every uid in the canary at stage k is also in the canary at every later stage.
func TestCohortsAreNested(t *testing.T) {
	for _, uid := range randomUIDs(10000, 1) {
		b := Bucket(uid)
		for i, w := range stages {
			if !ToCanary(b, Threshold(w)) {
				continue
			}
			for _, later := range stages[i+1:] {
				if !ToCanary(b, Threshold(later)) {
					t.Fatalf("uid %s (bucket %d) in canary at w=%v but not at w=%v", uid, b, w, later)
				}
			}
		}
	}
}

func share(uids []string, w float64) float64 {
	n := 0
	th := Threshold(w)
	for _, u := range uids {
		if ToCanary(Bucket(u), th) {
			n++
		}
	}
	return float64(n) / float64(len(uids))
}

// Realized canary share for 100,000 random uids is within +/-0.5 pp of w.
func TestRealizedShareRandomUIDs(t *testing.T) {
	uids := randomUIDs(100000, 2)
	for _, w := range stages {
		s := share(uids, w)
		t.Logf("random uids: w=%.2f share=%.4f", w, s)
		if math.Abs(s-w) > 0.005 {
			t.Errorf("w=%v: share %.4f outside +/-0.005", w, s)
		}
	}
}

// Same check on the exact k6 pool (user-00000 .. user-09999).
func TestRealizedShareLoadPool(t *testing.T) {
	uids := make([]string, 10000)
	for i := range uids {
		uids[i] = fmt.Sprintf("user-%05d", i)
	}
	for _, w := range stages {
		s := share(uids, w)
		t.Logf("k6 pool: w=%.2f share=%.4f", w, s)
		if math.Abs(s-w) > 0.005 {
			t.Errorf("w=%v: pool share %.4f outside +/-0.005", w, s)
		}
	}
}

func TestThresholdEdges(t *testing.T) {
	if Threshold(0) != 0 || Threshold(1) != Buckets || Threshold(0.01) != 100 || Threshold(0.00005) != 1 {
		t.Fatal("unexpected thresholds")
	}
	for _, u := range randomUIDs(1000, 3) {
		b := Bucket(u)
		if b < 0 || b >= Buckets || ToCanary(b, Threshold(0)) || !ToCanary(b, Threshold(1)) {
			t.Fatalf("bucket %d edge behaviour wrong", b)
		}
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) lines(t *testing.T, event string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(s.b.Bytes()))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("bad log line %q", sc.Text())
		}
		if m["event"] == event {
			out = append(out, m)
		}
	}
	return out
}

func upstream(name string, code int, delay time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
		w.Header().Set("X-Upstream", name)
		w.Header().Set("X-Seen-UID", r.Header.Get(headerUID))
		w.WriteHeader(code)
		io.WriteString(w, name)
	}))
}

func newTestRouter(t *testing.T, canaryCode int, canaryDelay time.Duration) (*httptest.Server, *syncBuf) {
	t.Helper()
	st, ca := upstream("stable", 200, 0), upstream("canary", canaryCode, canaryDelay)
	t.Cleanup(st.Close)
	t.Cleanup(ca.Close)
	logs := &syncBuf{}
	r, err := NewRouter(Config{StableURL: st.URL, CanaryURL: ca.URL, SLOLatency: 50 * time.Millisecond}, jlog.New(logs))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(r.Handler())
	t.Cleanup(ts.Close)
	return ts, logs
}

func putWeight(t *testing.T, base, body string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, base+"/admin/weight", strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func get(t *testing.T, url, uid string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(`{}`))
	if uid != "" {
		req.Header.Set(headerUID, uid)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

func TestRoutingWeightAndLogs(t *testing.T) {
	ts, logs := newTestRouter(t, 200, 0)
	uid := "user-00042"

	if resp := get(t, ts.URL+"/checkout", uid); resp.Header.Get(headerServedBy) != "stable" || resp.Header.Get("X-Upstream") != "stable" {
		t.Fatalf("weight 0 must route to stable, got %q", resp.Header.Get(headerServedBy))
	}
	if code := putWeight(t, ts.URL, `{"weight":1.0,"stage":4,"reason":"test promote"}`); code != 200 {
		t.Fatalf("put weight: %d", code)
	}
	resp := get(t, ts.URL+"/checkout", uid)
	if resp.Header.Get(headerServedBy) != "canary" || resp.Header.Get("X-Upstream") != "canary" || resp.Header.Get("X-Seen-UID") != uid {
		t.Fatalf("weight 1 must route to canary with uid forwarded, got %v", resp.Header)
	}
	for _, bad := range []string{`{"weight":1.5}`, `{"stage":1}`, `{"weight":-0.1}`, `nope`} {
		if code := putWeight(t, ts.URL, bad); code != 400 {
			t.Errorf("%s: want 400, got %d", bad, code)
		}
	}

	wc := logs.lines(t, "weight_change")
	if len(wc) != 2 || wc[1]["old"] != 0.0 || wc[1]["new"] != 1.0 || wc[1]["stage"] != 4.0 || wc[1]["reason"] != "test promote" {
		t.Fatalf("weight_change log: %v", wc)
	}
	rq := logs.lines(t, "request")
	if len(rq) != 2 {
		t.Fatalf("want 2 request lines, got %d", len(rq))
	}
	for _, k := range []string{"ts", "uid", "bucket", "version", "status", "latency_ms", "slo_violation"} {
		if _, ok := rq[1][k]; !ok {
			t.Errorf("request log missing %q", k)
		}
	}
	if rq[0]["version"] != "stable" || rq[1]["version"] != "canary" || rq[1]["bucket"] != float64(Bucket(uid)) {
		t.Fatalf("request log: %v", rq)
	}
	if !tsOK(rq[1]["ts"]) || !tsOK(wc[1]["ts"]) {
		t.Fatal("timestamps must be UTC RFC3339 millis")
	}
}

func tsOK(v any) bool {
	s, _ := v.(string)
	_, err := time.Parse("2006-01-02T15:04:05.000Z", s)
	return err == nil && strings.HasSuffix(s, "Z")
}

func TestSLOViolationAndMetrics(t *testing.T) {
	ts, logs := newTestRouter(t, 500, 0)
	putWeight(t, ts.URL, `{"weight":1,"stage":4,"reason":"t"}`)
	get(t, ts.URL+"/checkout", "u1") // canary 500 -> violation
	rq := logs.lines(t, "request")
	if rq[0]["slo_violation"] != true || rq[0]["status"] != 500.0 {
		t.Fatalf("500 must be an SLO violation: %v", rq[0])
	}
	resp, _ := http.Get(ts.URL + "/metrics")
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{
		`router_requests_total{code="500",version="canary"} 1`,
		`router_slo_violations_total{version="canary"} 1`,
		`router_slo_violations_total{version="stable"} 0`,
		`router_canary_weight 1`,
		`router_request_duration_seconds_count{version="canary"} 1`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}

func TestSlowRequestIsViolation(t *testing.T) {
	ts, logs := newTestRouter(t, 200, 80*time.Millisecond) // SLO 50 ms in tests
	putWeight(t, ts.URL, `{"weight":1,"stage":4,"reason":"t"}`)
	get(t, ts.URL+"/checkout", "u1")
	if rq := logs.lines(t, "request"); rq[0]["slo_violation"] != true || rq[0]["status"] != 200.0 {
		t.Fatalf("slow 200 must be an SLO violation: %v", rq[0])
	}
}

func TestMissingUIDIsGenerated(t *testing.T) {
	ts, logs := newTestRouter(t, 200, 0)
	resp := get(t, ts.URL+"/checkout", "")
	gen := resp.Header.Get(headerUID)
	if resp.Header.Get(headerGenerated) != "true" || !strings.HasPrefix(gen, "anon-") || resp.Header.Get("X-Seen-UID") != gen {
		t.Fatalf("generated uid not marked/forwarded: %v", resp.Header)
	}
	if rq := logs.lines(t, "request"); rq[0]["uid_generated"] != true || rq[0]["uid"] != gen {
		t.Fatalf("log: %v", rq[0])
	}
	if resp := get(t, ts.URL+"/checkout", "user-1"); resp.Header.Get(headerGenerated) != "" {
		t.Fatal("supplied uid must not be marked generated")
	}
}
