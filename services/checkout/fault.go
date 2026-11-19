package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"dbrcl-testbed/services/internal/jlog"
)

// Fault names. Only one fault is active at a time.
var knownFaults = []string{
	"mem_leak", "cpu_saturation", "latency_regression",
	"dependency_failure", "error_burst", "unexpected_egress",
}

// The only network target unexpected_egress may touch.
const (
	sinkHostInCluster = "egress-sink.sink.svc.cluster.local"
	sinkPort          = 9000
)

var errInjected = errors.New("injected dependency failure")

// faultConfig is the validated, defaulted parameter set of the active fault.
type faultConfig struct {
	Name string

	// mem_leak
	TargetFraction float64
	TauSeconds     float64
	// cpu_saturation
	Workers int
	Duty    float64
	// latency_regression
	MedianMs float64
	Sigma    float64
	// dependency_failure, error_burst, unexpected_egress
	P    float64
	Mode string
	// unexpected_egress
	ExtraPorts []int
}

// effectiveParams is what gets logged as ground truth.
func (c faultConfig) effectiveParams() map[string]any {
	switch c.Name {
	case "mem_leak":
		return map[string]any{"target_fraction": c.TargetFraction, "tau_seconds": c.TauSeconds}
	case "cpu_saturation":
		return map[string]any{"workers": c.Workers, "duty": c.Duty}
	case "latency_regression":
		return map[string]any{"median_ms": c.MedianMs, "sigma": c.Sigma}
	case "dependency_failure":
		return map[string]any{"p": c.P, "mode": c.Mode}
	case "error_burst":
		return map[string]any{"p": c.P}
	case "unexpected_egress":
		return map[string]any{"p": c.P, "extra_ports": c.ExtraPorts}
	}
	return map[string]any{}
}

func num(params map[string]any, key string, def float64) (float64, error) {
	v, ok := params[key]
	if !ok || v == nil {
		return def, nil
	}
	switch x := v.(type) {
	case float64:
		return x, nil
	case int:
		return float64(x), nil
	case string:
		return strconv.ParseFloat(x, 64)
	}
	return 0, fmt.Errorf("param %s: not a number", key)
}

func inRange(key string, v, lo, hi float64) error {
	if math.IsNaN(v) || v < lo || v > hi {
		return fmt.Errorf("param %s=%v out of range [%v, %v]", key, v, lo, hi)
	}
	return nil
}

// parseFault validates params and fills defaults.
func parseFault(name string, params map[string]any) (faultConfig, error) {
	c := faultConfig{Name: name}
	var err error
	get := func(key string, def, lo, hi float64) float64 {
		if err != nil {
			return 0
		}
		var v float64
		if v, err = num(params, key, def); err == nil {
			err = inRange(key, v, lo, hi)
		}
		return v
	}
	switch name {
	case "mem_leak":
		c.TargetFraction = get("target_fraction", 0.9, 0.01, 0.99)
		c.TauSeconds = get("tau_seconds", 300, 0.001, 86400)
	case "cpu_saturation":
		c.Workers = int(get("workers", 2, 1, 64))
		c.Duty = get("duty", 0.9, 0.01, 1)
	case "latency_regression":
		c.MedianMs = get("median_ms", 250, 0, 60000)
		c.Sigma = get("sigma", 0.5, 0, 5)
	case "dependency_failure":
		c.P = get("p", 0.3, 0, 1)
		c.Mode = "error"
		if m, ok := params["mode"]; ok {
			s, _ := m.(string)
			if s != "error" && s != "timeout" {
				return c, fmt.Errorf("param mode must be error|timeout")
			}
			c.Mode = s
		}
	case "error_burst":
		c.P = get("p", 0.3, 0, 1)
	case "unexpected_egress":
		c.P = get("p", 0.2, 0, 1)
		c.ExtraPorts = []int{}
		if raw, ok := params["extra_ports"]; ok && raw != nil {
			list, ok := raw.([]any)
			if !ok {
				return c, fmt.Errorf("param extra_ports must be a list")
			}
			for _, x := range list {
				f, ok := x.(float64)
				if !ok || f != math.Trunc(f) || f < 1 || f > 65535 {
					return c, fmt.Errorf("param extra_ports: invalid port %v", x)
				}
				c.ExtraPorts = append(c.ExtraPorts, int(f))
			}
		}
	default:
		return c, fmt.Errorf("unknown fault %q", name)
	}
	return c, err
}

// Engine holds the active fault and its background workers.
type Engine struct {
	pod      string
	memLimit int64
	log      *jlog.Logger
	onChange func(name string, active bool)

	mu    sync.Mutex
	cfg   faultConfig // Name == "" when no fault is active
	since time.Time
	stop  chan struct{}
	wg    sync.WaitGroup

	// mem_leak state
	leakMu    sync.Mutex
	leak      [][]byte
	retained  atomic.Int64
	chunk     int64
	leakTick  time.Duration
	cpuActive atomic.Int32

	// unexpected_egress target; fixed to the in-cluster sink, overridden only by tests in this package.
	sinkHost string
	sinkPort int
	dialed   atomic.Int64
}

// NewEngine returns an engine with no active fault.
func NewEngine(pod string, memLimit int64, l *jlog.Logger, onChange func(string, bool)) *Engine {
	if onChange == nil {
		onChange = func(string, bool) {}
	}
	return &Engine{
		pod: pod, memLimit: memLimit, log: l, onChange: onChange,
		chunk: 1 << 20, leakTick: time.Second, sinkHost: sinkHostInCluster, sinkPort: sinkPort,
	}
}

// Current returns the active config and activation time.
func (e *Engine) Current() (faultConfig, time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg, e.since
}

// RetainedBytes is the memory currently held by mem_leak.
func (e *Engine) RetainedBytes() int64 { return e.retained.Load() }

// Activate validates and starts a fault, replacing any active one.
func (e *Engine) Activate(name string, params map[string]any) error {
	cfg, err := parseFault(name, params)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.deactivateLocked()
	e.cfg = cfg
	e.since = time.Now()
	e.stop = make(chan struct{})
	switch name {
	case "mem_leak":
		target := int64(cfg.TargetFraction * float64(e.memLimit))
		e.wg.Add(1)
		go e.runLeak(e.stop, target, cfg.TauSeconds)
	case "cpu_saturation":
		for i := 0; i < cfg.Workers; i++ {
			e.wg.Add(1)
			go e.runCPU(e.stop, cfg.Duty)
		}
	}
	e.onChange(name, true)
	e.log.Log(map[string]any{"ts": jlog.Now(), "event": "fault_on", "fault": name, "params": cfg.effectiveParams(), "pod": e.pod})
	return nil
}

// Deactivate stops the active fault, if any.
func (e *Engine) Deactivate() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.deactivateLocked()
}

func (e *Engine) deactivateLocked() {
	if e.cfg.Name == "" {
		return
	}
	close(e.stop)
	e.wg.Wait()
	if e.cfg.Name == "mem_leak" {
		e.leakMu.Lock()
		e.leak = nil
		e.leakMu.Unlock()
		e.retained.Store(0)
		debug.FreeOSMemory()
	}
	old := e.cfg
	e.cfg = faultConfig{}
	e.since = time.Time{}
	e.onChange(old.Name, false)
	e.log.Log(map[string]any{"ts": jlog.Now(), "event": "fault_off", "fault": old.Name, "params": old.effectiveParams(), "pod": e.pod})
}

// runLeak grows retained memory as target*(1-exp(-t/tau)).
func (e *Engine) runLeak(stop chan struct{}, target int64, tau float64) {
	defer e.wg.Done()
	start := time.Now()
	t := time.NewTicker(e.leakTick)
	defer t.Stop()
	for {
		want := int64(float64(target) * (1 - math.Exp(-time.Since(start).Seconds()/tau)))
		for e.retained.Load()+e.chunk <= want {
			b := make([]byte, e.chunk)
			for i := 0; i < len(b); i += 4096 { // touch every page so it counts in RSS
				b[i] = 1
			}
			e.leakMu.Lock()
			e.leak = append(e.leak, b)
			e.leakMu.Unlock()
			e.retained.Add(e.chunk)
		}
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

// runCPU burns CPU for duty*period of every 100 ms period.
func (e *Engine) runCPU(stop chan struct{}, duty float64) {
	defer e.wg.Done()
	e.cpuActive.Add(1)
	defer e.cpuActive.Add(-1)
	const period = 100 * time.Millisecond
	busy := time.Duration(duty * float64(period))
	x := 0
	for {
		end := time.Now().Add(busy)
		for time.Now().Before(end) {
			x++
		}
		select {
		case <-stop:
			return
		case <-time.After(period - busy):
		}
	}
}

// requestEffects is what a fault does to one incoming checkout request.
type requestEffects struct {
	ExtraLatency time.Duration
	ForceStatus  int // 0 = no override
}

// OnRequest applies per-request faults. Egress is fired asynchronously.
func (e *Engine) OnRequest() requestEffects {
	cfg, _ := e.Current()
	var fx requestEffects
	switch cfg.Name {
	case "latency_regression":
		ms := cfg.MedianMs * math.Exp(cfg.Sigma*rand.NormFloat64())
		fx.ExtraLatency = time.Duration(ms * float64(time.Millisecond))
	case "error_burst":
		if rand.Float64() < cfg.P {
			fx.ForceStatus = 500
		}
	case "unexpected_egress":
		if rand.Float64() < cfg.P {
			go e.touchSink(cfg.ExtraPorts)
		}
	}
	return fx
}

// OnPayment decides whether the payment call from this pod fails. It returns
// (fail, timeout): with timeout the caller waits its client timeout first.
func (e *Engine) OnPayment() (fail bool, timeout bool) {
	cfg, _ := e.Current()
	if cfg.Name != "dependency_failure" || rand.Float64() >= cfg.P {
		return false, false
	}
	return true, cfg.Mode == "timeout"
}

// touchSink opens a short TCP connection to the in-cluster sink (and optional
// extra ports on the same host), writes a few bytes and closes.
func (e *Engine) touchSink(extra []int) {
	ports := append([]int{e.sinkPort}, extra...)
	for _, p := range ports {
		c, err := net.DialTimeout("tcp", net.JoinHostPort(e.sinkHost, strconv.Itoa(p)), 500*time.Millisecond)
		if err != nil {
			continue
		}
		c.SetDeadline(time.Now().Add(500 * time.Millisecond))
		c.Write([]byte("dbrcl-egress-test\n"))
		c.Close()
		e.dialed.Add(1)
	}
}
