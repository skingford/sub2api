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
	failures     atomic.Uint64
	events       atomic.Uint64
	bytes        atomic.Uint64
	encodeNanos  atomic.Uint64
	waitNanos    atomic.Uint64
	writeNanos   atomic.Uint64
}

type Trace struct {
	recorder *Recorder
	ID       string
	started  time.Time
	mu       sync.Mutex
	sequence uint64 // guarded by trace.mu
	attempts atomic.Int64
	failures uint64 // guarded by trace.mu
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
	err := r.writer.Close()
	r.mu.Unlock()
	stats := r.Stats()
	slog.Info("gateway request trace recorder summary", "events", stats.Events,
		"failures", stats.Failures, "bytes", stats.Bytes, "encode_time", stats.EncodeTime,
		"lock_wait", stats.LockWait, "write_time", stats.WriteTime)
	return err
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

// Stats contains cumulative recorder costs. Timings are summed across events,
// so lock wait can exceed wall time when requests run concurrently.
type Stats struct {
	Events     uint64        `json:"events"`
	Failures   uint64        `json:"failures"`
	Bytes      uint64        `json:"bytes"`
	EncodeTime time.Duration `json:"encode_ns"`
	LockWait   time.Duration `json:"lock_wait_ns"`
	WriteTime  time.Duration `json:"write_ns"`
}

func (r *Recorder) Stats() Stats {
	if r == nil {
		return Stats{}
	}
	return Stats{Events: r.events.Load(), Failures: r.failures.Load(), Bytes: r.bytes.Load(),
		EncodeTime: time.Duration(r.encodeNanos.Load()), LockWait: time.Duration(r.waitNanos.Load()), WriteTime: time.Duration(r.writeNanos.Load())}
}

// Event writes synchronously: no queue, sampling or silent queue overflow.
// Each trace keeps sequence/failure ordering. Independent traces encode before
// taking the recorder's write lock; a slow disk still applies backpressure.
// recorder_write_failures is the global snapshot at event encoding time.
func (t *Trace) Event(kind string, fields map[string]any) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.recorder
	r.events.Add(1)
	t.sequence++
	now := time.Now()
	event := struct {
		Schema           int            `json:"schema"`
		Time             time.Time      `json:"time"`
		InstanceID       string         `json:"instance_id"`
		TraceID          string         `json:"trace_id"`
		Seq              uint64         `json:"seq"`
		ElapsedMS        float64        `json:"elapsed_ms"`
		Event            string         `json:"event"`
		Data             map[string]any `json:"data"`
		TraceFailures    uint64         `json:"trace_write_failures"`
		RecorderFailures uint64         `json:"recorder_write_failures"`
	}{1, now.UTC(), r.instance, t.ID, t.sequence, float64(now.Sub(t.started).Microseconds()) / 1000, kind, fields, t.failures, r.failures.Load()}
	b, err := json.Marshal(event)
	r.encodeNanos.Add(uint64(time.Since(now)))
	if err == nil {
		b = append(b, '\n')
	}
	waiting := time.Now()
	r.mu.Lock()
	r.waitNanos.Add(uint64(time.Since(waiting)))
	if err == nil {
		writing := time.Now()
		n, writeErr := r.writer.Write(b)
		r.writeNanos.Add(uint64(time.Since(writing)))
		if n > 0 {
			r.bytes.Add(uint64(n))
		}
		err = writeErr
		if err == nil && n != len(b) {
			err = io.ErrShortWrite
		}
	}
	var warn bool
	var failures uint64
	if err != nil {
		t.failures++
		failures = r.failures.Add(1)
		if time.Since(r.lastWarning) >= time.Minute {
			r.lastWarning = time.Now()
			warn = true
		}
	}
	r.mu.Unlock()
	// Operational logging must not hold the shared disk lock.
	if warn {
		slog.Error("gateway request trace write failed", "error", err, "failed_events", failures)
	}
	return err == nil
}
