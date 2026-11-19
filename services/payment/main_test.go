package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPayLatencyAndMetrics(t *testing.T) {
	s := newServer(20*time.Millisecond, 0, "test")
	ts := httptest.NewServer(s.handler())
	defer ts.Close()

	start := time.Now()
	resp, err := http.Post(ts.URL+"/pay", "application/json", strings.NewReader(`{"order_id":"o1","amount_cents":100}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if el := time.Since(start); el < 20*time.Millisecond {
		t.Fatalf("latency %v < 20ms", el)
	}

	resp, _ = http.Post(ts.URL+"/pay", "application/json", strings.NewReader(`{"amount_cents":-1}`))
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("negative amount: status %d", resp.StatusCode)
	}

	resp, _ = http.Get(ts.URL + "/metrics")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{`payment_requests_total{code="200",pod="test"} 1`, `payment_requests_total{code="400",pod="test"} 1`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}
