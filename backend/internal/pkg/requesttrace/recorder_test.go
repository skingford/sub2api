package requesttrace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type memoryWriter struct{ bytes.Buffer }

func (*memoryWriter) Close() error { return nil }

type event struct {
	Event    string         `json:"event"`
	TraceID  string         `json:"trace_id"`
	Seq      int            `json:"seq"`
	Data     map[string]any `json:"data"`
	Failures int            `json:"trace_write_failures"`
}

func readEvents(t *testing.T, data []byte) []event {
	t.Helper()
	var events []event
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var e event
		require.NoError(t, dec.Decode(&e))
		events = append(events, e)
	}
	return events
}

func bodyEvents(t *testing.T, events []event, stream string, attempt int64) ([]byte, map[string]any) {
	t.Helper()
	var body []byte
	var end map[string]any
	for _, e := range events {
		if e.Data["stream"] != stream || e.Data["attempt"] != float64(attempt) {
			continue
		}
		if e.Event == "body.chunk" {
			require.Equal(t, float64(len(body)), e.Data["offset"])
			encoded, ok := e.Data["base64"].(string)
			require.True(t, ok)
			part, err := base64.StdEncoding.DecodeString(encoded)
			require.NoError(t, err)
			body = append(body, part...)
		}
		if e.Event == "body.end" {
			end = e.Data
		}
	}
	return body, end
}

func TestRequestTraceBodyIntegrityAndLimits(t *testing.T) {
	for _, limit := range []int64{0, 37} {
		t.Run(string(rune('A'+limit)), func(t *testing.T) {
			w := &memoryWriter{}
			r := &Recorder{writer: w, maxBodyBytes: limit}
			_, trace := r.Start(t.Context())
			body := bytes.Repeat([]byte{0xff, 0, '\n', 'x'}, 50000)
			s := trace.Stream("test", 1, int64(len(body)))
			wrapped := CaptureBody(io.NopCloser(bytes.NewReader(body)), s)
			got, err := io.ReadAll(wrapped)
			require.NoError(t, err)
			require.Equal(t, body, got)
			require.NoError(t, wrapped.Close())
			captured, end := bodyEvents(t, readEvents(t, w.Bytes()), "test", 1)
			if limit == 0 {
				require.Equal(t, body, captured)
			} else {
				require.Equal(t, body[:limit], captured)
			}
			hash := sha256.Sum256(body)
			require.Equal(t, hex.EncodeToString(hash[:]), end["sha256"])
			require.Equal(t, true, end["complete"])
			require.Equal(t, limit > 0, end["truncated"])
		})
	}
}

func TestRequestTraceEarlyCloseAndReadError(t *testing.T) {
	for _, readError := range []bool{false, true} {
		w := &memoryWriter{}
		r := &Recorder{writer: w}
		_, trace := r.Start(t.Context())
		s := trace.Stream("test", 0, 100)
		s.Write([]byte("partial"))
		if readError {
			s.Finish("read_error", io.ErrUnexpectedEOF)
		} else {
			s.Finish("closed", nil)
		}
		_, end := bodyEvents(t, readEvents(t, w.Bytes()), "test", 0)
		require.Equal(t, false, end["complete"])
	}
}

func TestRequestTraceRedactionDoesNotMutateProtocolFields(t *testing.T) {
	h := http.Header{"authorization": {"Bearer test-secret"}, "X-API-Key": {"key-secret"}, "Cookie": {"cookie-secret"}, "Set-Cookie": {"response-cookie"}, "Proxy-Authorization": {"proxy-secret"}, "Anthropic-Beta": {"test-beta"}, "X-Claude-Code-Session-Id": {"session"}, "anthropic-ratelimit-tokens-remaining": {"42"}}
	copy := Headers(h)
	require.Equal(t, []string{"[REDACTED]"}, copy["authorization"])                //nolint:staticcheck // SA1008: deliberately exercise preserved noncanonical wire spelling.
	require.Equal(t, "key-secret", h["X-API-Key"][0])                              //nolint:staticcheck // SA1008: deliberately exercise preserved noncanonical wire spelling.
	require.Equal(t, []string{"42"}, copy["anthropic-ratelimit-tokens-remaining"]) //nolint:staticcheck // SA1008: deliberately exercise preserved noncanonical wire spelling.
	require.Equal(t, "session", copy.Get("X-Claude-Code-Session-Id"))
	u, err := url.Parse("https://user:password@example.test/v1/messages?key=secret&access_tokens=private&beta=true")
	require.NoError(t, err)
	safe := URL(u)
	for _, secret := range []string{"password", "secret", "private"} {
		require.NotContains(t, safe, secret)
	}
	require.Contains(t, safe, "beta=true")
	require.Contains(t, u.String(), "secret")
	redirect := Headers(http.Header{"Location": {u.String()}})
	require.NotContains(t, redirect.Get("Location"), "secret")
	require.NotContains(t, redirect.Get("Location"), "private")
	err = &url.Error{Op: "Post", URL: u.String(), Err: errors.New("connect https://user:password@proxy.test?token=secret failed")}
	require.NotContains(t, SafeError(err), "password")
	require.NotContains(t, SafeError(err), "secret")
}

func TestRequestTraceTransportRedirectAndStreaming(t *testing.T) {
	w := &memoryWriter{}
	recorder := &Recorder{writer: w}
	ctx, _ := recorder.Start(context.Background())
	payload := "data: {\"type\":\"message_start\"}\n\ndata: [DONE]\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/messages", http.StatusTemporaryRedirect)
			return
		}
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, "original", string(body))
		require.Equal(t, "Bearer test-secret", r.Header.Get("Authorization"))
		w.Header().Set("Request-Id", "req-evidence")
		w.Header().Set("Trailer", "X-Trace-End")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, payload)
		w.Header().Set("X-Trace-End", "finished")
	}))
	defer server.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/redirect", strings.NewReader("original"))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer test-secret")
	req = WithUpstream(req, 17, 2, "http://user:proxy-secret@proxy.test:8080", "native")
	client := InstrumentClient(server.Client(), req)
	resp, err := client.Do(req)
	require.NoError(t, err)
	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, payload, string(got))
	require.NoError(t, resp.Body.Close())
	events := readEvents(t, w.Bytes())
	for _, attempt := range []int64{1, 2} {
		body, end := bodyEvents(t, events, "upstream.request", attempt)
		require.Equal(t, "original", string(body))
		require.Equal(t, true, end["complete"])
	}
	body, end := bodyEvents(t, events, "upstream.response", 2)
	require.Equal(t, payload, string(body))
	require.Equal(t, true, end["complete"])
	require.NotContains(t, w.String(), "test-secret")
	require.NotContains(t, w.String(), "proxy-secret")
	require.Contains(t, w.String(), "req-evidence")
	require.Contains(t, w.String(), "finished")
}

func TestRequestTraceConcurrentIsolation(t *testing.T) {
	w := &memoryWriter{}
	recorder := &Recorder{writer: w}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, trace := recorder.Start(context.Background())
			for j := 0; j < 30; j++ {
				trace.Event("test", map[string]any{"id": trace.ID})
			}
		}()
	}
	wg.Wait()
	seq := map[string]int{}
	for _, e := range readEvents(t, w.Bytes()) {
		seq[e.TraceID]++
		require.Equal(t, seq[e.TraceID], e.Seq)
		require.Equal(t, e.TraceID, e.Data["id"])
	}
	require.Len(t, seq, 40)
}

func TestRequestTraceRotationPermissionsAndDisabled(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	r, err := New(config.RequestTraceConfig{Enabled: true, Directory: dir, MaxSizeMB: 1, MaxBackups: 3, MaxAgeDays: 1})
	require.NoError(t, err)
	_, trace := r.Start(t.Context())
	s := trace.Stream("test", 0, -1)
	s.Write(bytes.Repeat([]byte("x"), 1200000))
	s.Finish("eof", nil)
	require.NoError(t, r.Close())
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	require.NoError(t, err)
	require.Greater(t, len(files), 1)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0700), info.Mode().Perm())
	for _, path := range files {
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}
	disabled, err := New(config.RequestTraceConfig{Directory: filepath.Join(t.TempDir(), "absent")})
	require.NoError(t, err)
	require.Nil(t, disabled)
}

type failingWriter struct {
	memoryWriter
	fail bool
}

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.fail {
		return 0, errors.New("disk full")
	}
	return w.memoryWriter.Write(p)
}

func TestRequestTraceDiskFailuresRemainVisible(t *testing.T) {
	w := &failingWriter{fail: true}
	recorder := &Recorder{writer: w}
	_, trace := recorder.Start(t.Context())
	trace.Event("lost", nil)
	w.fail = false
	trace.Event("recovered", nil)
	events := readEvents(t, w.Bytes())
	require.Len(t, events, 1)
	require.Equal(t, 1, events[0].Failures)
	require.Equal(t, 2, events[0].Seq)
}

type traceEncodingSignal struct{ started chan struct{} }

func (s traceEncodingSignal) MarshalJSON() ([]byte, error) {
	close(s.started)
	return []byte(`"encoded"`), nil
}

type firstBlockingTraceWriter struct {
	memoryWriter
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *firstBlockingTraceWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started); <-w.release })
	return w.memoryWriter.Write(p)
}
func TestRequestTraceEncodesIndependentTracesOutsideDiskLock(t *testing.T) {
	w := &firstBlockingTraceWriter{started: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(w.release) }) })
	r := &Recorder{writer: w}
	_, a := r.Start(t.Context())
	_, b := r.Start(t.Context())
	done := make(chan bool, 2)
	go func() { done <- a.Event("first", nil) }()
	select {
	case <-w.started:
	case <-time.After(time.Second):
		t.Fatal("writer not started")
	}
	encoded := make(chan struct{})
	go func() { done <- b.Event("second", map[string]any{"signal": traceEncodingSignal{encoded}}) }()
	select {
	case <-encoded:
	case <-time.After(time.Second):
		t.Fatal("unrelated trace encoding blocked by disk lock")
	}
	release.Do(func() { close(w.release) })
	require.True(t, <-done)
	require.True(t, <-done)
	require.Len(t, readEvents(t, w.Bytes()), 2)
	stats := r.Stats()
	require.EqualValues(t, 2, stats.Events)
	require.Zero(t, stats.Failures)
	require.EqualValues(t, w.Len(), stats.Bytes)
	require.Positive(t, stats.EncodeTime)
	require.Positive(t, stats.WriteTime)
}
func TestRequestTraceSameTraceConcurrentSequenceAndStats(t *testing.T) {
	w := &memoryWriter{}
	r := &Recorder{writer: w}
	_, trace := r.Start(t.Context())
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() { defer wg.Done(); trace.Event("concurrent", nil) }()
	}
	wg.Wait()
	for i, e := range readEvents(t, w.Bytes()) {
		require.Equal(t, i+1, e.Seq)
	}
	require.EqualValues(t, 32, r.Stats().Events)
	require.Zero(t, r.Stats().Failures)
	require.False(t, trace.Event("unserializable", map[string]any{"bad": make(chan int)}))
	require.EqualValues(t, 1, r.Stats().Failures)
	require.True(t, trace.Event("recovered", nil))
	last := readEvents(t, w.Bytes())[32]
	require.Equal(t, 34, last.Seq)
	require.Equal(t, 1, last.Failures)
}
