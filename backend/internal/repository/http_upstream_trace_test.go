package repository

import (
	"bytes"
	"compress/gzip"
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
	"github.com/stretchr/testify/require"
)

func TestRequestTraceRepositoryCapturesCompressedErrorBeforeDecoding(t *testing.T) {
	dir := t.TempDir()
	recorder, err := requesttrace.New(config.RequestTraceConfig{Enabled: true, Directory: dir, MaxSizeMB: 10, MaxBackups: 2, MaxAgeDays: 1})
	require.NoError(t, err)
	ctx, _ := recorder.Start(t.Context())
	response := `{"type":"error","error":{"type":"permission_error","message":"synthetic account disabled"},"request_id":"req-ban-test"}`
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	_, err = gz.Write([]byte(response))
	require.NoError(t, err)
	require.NoError(t, gz.Close())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, `{"model":"claude-test","stream":true}`, string(body))
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Request-Id", "req-ban-test")
		w.Header().Set("Anthropic-Ratelimit-Tokens-Remaining", "0")
		w.Header().Set("Set-Cookie", "credential-secret")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write(compressed.Bytes())
	}))
	defer server.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader(`{"model":"claude-test","stream":true}`))
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := NewHTTPUpstream(nil).Do(req, "", 123, 2)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, response, string(body))
	require.Equal(t, 403, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
	require.NoError(t, recorder.Close())
	data, err := os.ReadFile(filepath.Join(dir, "requests.jsonl"))
	require.NoError(t, err)
	require.NotContains(t, string(data), "credential-secret")
	bodies := map[string][]byte{}
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var e struct {
			Event string
			Data  map[string]any
		}
		require.NoError(t, dec.Decode(&e))
		if e.Event == "body.chunk" {
			encoded, ok := e.Data["base64"].(string)
			require.True(t, ok)
			part, err := base64.StdEncoding.DecodeString(encoded)
			require.NoError(t, err)
			name, ok := e.Data["stream"].(string)
			require.True(t, ok)
			bodies[name] = append(bodies[name], part...)
		}
	}
	require.Equal(t, compressed.Bytes(), bodies["upstream.response"])
	require.Equal(t, response, string(bodies["upstream.response_decoded"]))
	require.Contains(t, string(data), "req-ban-test")
	require.Contains(t, string(data), `"account_id":123`)
	require.Contains(t, string(data), "Anthropic-Ratelimit-Tokens-Remaining")
}

func TestRequestTraceNativeTransportRetainsWireHeaders(t *testing.T) {
	dir := t.TempDir()
	recorder, err := requesttrace.New(config.RequestTraceConfig{Enabled: true, Directory: dir, MaxSizeMB: 10, MaxBackups: 2, MaxAgeDays: 1})
	require.NoError(t, err)
	ctx, _ := recorder.Start(t.Context())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer real-fake-token", r.Header.Get("Authorization"))
		w.WriteHeader(204)
	}))
	defer server.Close()
	transport := newNativeClaudeHTTP1Transport(&http.Transport{})
	defer transport.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader("native body"))
	require.NoError(t, err)
	req.Header["authorization"] = []string{"Bearer real-fake-token"}      //nolint:staticcheck // SA1008: native protocol deliberately uses raw header spelling.
	req.Header["x-claude-code-session-id"] = []string{"session-evidence"} //nolint:staticcheck // SA1008: native protocol deliberately uses raw header spelling.
	resp, err := requesttrace.InstrumentClient(&http.Client{Transport: transport}, req).Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.NoError(t, recorder.Close())
	data, err := os.ReadFile(filepath.Join(dir, "requests.jsonl"))
	require.NoError(t, err)
	require.Contains(t, string(data), "upstream.native_headers")
	require.Contains(t, string(data), "upstream.connection")
	require.Contains(t, string(data), "upstream.first_byte")
	require.Contains(t, string(data), "session-evidence")
	require.NotContains(t, string(data), "real-fake-token")
}
