package middleware

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttrace"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestTraceMiddlewarePreservesSSEAndRecordsOriginalInput(t *testing.T) {
	dir := t.TempDir()
	recorder, err := requesttrace.New(config.RequestTraceConfig{Enabled: true, Directory: dir, MaxSizeMB: 10, MaxBackups: 2, MaxAgeDays: 1})
	require.NoError(t, err)
	r := gin.New()
	r.Use(RequestLogger(), ClientRequestID(), RequestTrace(recorder), RequestBodyLimit(1024))
	input := `{"model":"claude-test","metadata":{"user_id":"session-original"},"messages":[{"role":"user","content":"full input"}]}`
	output := "event: message_start\ndata: {\"id\":\"msg-test\"}\n\nevent: message_stop\ndata: {}\n\n"
	r.POST("/v1/messages", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		require.Equal(t, input, string(body))
		c.Request.Body = io.NopCloser(strings.NewReader("rewritten"))
		c.Header("Content-Type", "text/event-stream")
		c.Header("Request-Id", "provider-id")
		c.Writer.WriteHeader(http.StatusOK)
		_, err = c.Writer.WriteString(output[:20])
		require.NoError(t, err)
		c.Writer.Flush()
		_, err = c.Writer.Write([]byte(output[20:]))
		require.NoError(t, err)
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(input))
	req.Header.Set("Authorization", "Bearer client-secret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	require.Equal(t, output, w.Body.String())
	require.True(t, w.Flushed)
	require.NotEmpty(t, w.Header().Get("X-Sub2api-Trace-ID"))
	require.NoError(t, recorder.Close())
	data, err := os.ReadFile(filepath.Join(dir, "requests.jsonl"))
	require.NoError(t, err)
	require.NotContains(t, string(data), "client-secret")
	bodies := map[string]string{}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	for dec.More() {
		var e struct {
			Event string
			Data  map[string]any
		}
		require.NoError(t, dec.Decode(&e))
		if e.Event == "body.chunk" {
			encoded, ok := e.Data["base64"].(string)
			require.True(t, ok)
			body, err := base64.StdEncoding.DecodeString(encoded)
			require.NoError(t, err)
			name, ok := e.Data["stream"].(string)
			require.True(t, ok)
			bodies[name] += string(body)
		}
	}
	require.Equal(t, input, bodies["client.request"])
	require.Equal(t, output, bodies["client.response"])
}

func TestRequestTraceRejectDoesNotDrainBody(t *testing.T) {
	recorder, err := requesttrace.New(config.RequestTraceConfig{Enabled: true, Directory: t.TempDir(), MaxSizeMB: 10, MaxBackups: 2, MaxAgeDays: 1})
	require.NoError(t, err)
	defer func() { require.NoError(t, recorder.Close()) }()
	r := gin.New()
	r.Use(RequestTrace(recorder), RequestBodyLimit(10))
	r.POST("/v1/messages", func(c *gin.Context) {
		body, readErr := io.ReadAll(c.Request.Body)
		require.Error(t, readErr)
		require.Len(t, body, 10)
		c.AbortWithStatus(http.StatusRequestEntityTooLarge)
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(strings.Repeat("x", 20)))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	require.NotEmpty(t, w.Header().Get("X-Sub2api-Trace-ID"))
}

func TestRequestTraceDisabledIsTransparent(t *testing.T) {
	r := gin.New()
	r.Use(RequestTrace(nil))
	r.GET("/v1/models", func(c *gin.Context) { c.String(200, "ok") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	require.Equal(t, "ok", w.Body.String())
	require.Empty(t, w.Header().Get("X-Sub2api-Trace-ID"))
}
