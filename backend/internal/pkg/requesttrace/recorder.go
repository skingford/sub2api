// Package requesttrace records application HTTP exchanges without buffering or
// rewriting payloads. It is deliberately independent of sampled operational logs.
package requesttrace

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
	"gopkg.in/natefinch/lumberjack.v2"
)

type contextKey struct{}

type Recorder struct {
	mu           sync.Mutex
	writer       io.WriteCloser
	maxBodyBytes int64
	instance     string
	lastWarning  time.Time
	failures     uint64
}

type Trace struct {
	recorder *Recorder
	ID       string
	started  time.Time
	sequence uint64 // guarded by recorder.mu
	attempts atomic.Int64
	failures uint64 // guarded by recorder.mu
}

// New opens a private directory and file at startup, so an unwritable location
// is reported immediately. Each process must have its own directory.
func New(cfg config.RequestTraceConfig) (*Recorder, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	dir := cfg.Directory
	if dir == "" {
		data := os.Getenv("DATA_DIR")
		if data == "" {
			data = "/app/data"
		}
		dir = filepath.Join(data, "logs", "request-traces")
	}
	if err := os.MkdirAll(dir, 0700); err != nil { // #nosec G703 -- directory is trusted operator configuration, never request input.
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil { // #nosec G703 -- same operator-selected private log directory.
		return nil, err
	}
	path := filepath.Join(dir, "requests.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600) // #nosec G703 -- fixed filename under operator-selected directory.
	if err != nil {
		return nil, err
	}
	if err = f.Chmod(0600); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	return &Recorder{writer: &lumberjack.Logger{
		Filename: path, MaxSize: cfg.MaxSizeMB, MaxBackups: cfg.MaxBackups,
		MaxAge: cfg.MaxAgeDays, Compress: false,
	}, maxBodyBytes: cfg.MaxBodyBytes, instance: uuid.NewString()}, nil
}

func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writer.Close()
}

func (r *Recorder) Start(ctx context.Context) (context.Context, *Trace) {
	if r == nil {
		return ctx, nil
	}
	t := &Trace{recorder: r, ID: uuid.NewString(), started: time.Now()}
	return context.WithValue(ctx, contextKey{}, t), t
}

func FromContext(ctx context.Context) *Trace {
	if ctx == nil {
		return nil
	}
	t, _ := ctx.Value(contextKey{}).(*Trace)
	return t
}

// Event writes synchronously: no queue, sampling or silent queue overflow.
// Failures do not fail inference, but are counted on subsequent events and
// reported to the operational log at most once per minute.
func (t *Trace) Event(kind string, fields map[string]any) bool {
	if t == nil {
		return false
	}
	r := t.recorder
	r.mu.Lock()
	defer r.mu.Unlock()
	t.sequence++
	now := time.Now()
	event := map[string]any{
		"schema": 1, "time": now.UTC().Format(time.RFC3339Nano),
		"instance_id": r.instance, "trace_id": t.ID, "seq": t.sequence,
		"elapsed_ms": float64(now.Sub(t.started).Microseconds()) / 1000,
		"event":      kind, "data": fields, "trace_write_failures": t.failures,
		"recorder_write_failures": r.failures,
	}
	b, err := json.Marshal(event)
	if err == nil {
		b = append(b, '\n')
		var n int
		n, err = r.writer.Write(b)
		if err == nil && n != len(b) {
			err = io.ErrShortWrite
		}
	}
	if err != nil {
		t.failures++
		r.failures++
		if now.Sub(r.lastWarning) >= time.Minute {
			r.lastWarning = now
			slog.Error("gateway request trace write failed", "error", err, "failed_events", r.failures)
		}
		return false
	}
	return true
}
