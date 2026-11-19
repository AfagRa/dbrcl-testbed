// Package jlog writes one JSON object per line with a UTC RFC3339 millisecond
// timestamp. All timestamps used for results come through here.
package jlog

import (
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

// TSFormat is RFC3339 with milliseconds, always UTC ("Z").
const TSFormat = "2006-01-02T15:04:05.000Z07:00"

// Now returns the current UTC time formatted with TSFormat.
func Now() string { return time.Now().UTC().Format(TSFormat) }

// Logger serializes lines to a writer.
type Logger struct {
	mu sync.Mutex
	w  io.Writer
}

// New returns a Logger writing to w (os.Stdout if nil).
func New(w io.Writer) *Logger {
	if w == nil {
		w = os.Stdout
	}
	return &Logger{w: w}
}

// Log writes fields as one JSON line, adding "ts" if absent.
func (l *Logger) Log(fields map[string]any) {
	if _, ok := fields["ts"]; !ok {
		fields["ts"] = Now()
	}
	b, err := json.Marshal(fields)
	if err != nil {
		b = []byte(`{"ts":"` + Now() + `","event":"log_error"}`)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.w.Write(append(b, '\n'))
}
