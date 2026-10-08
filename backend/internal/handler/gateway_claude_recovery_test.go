//go:build unit

package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func recoveryWriterForTest(t *testing.T, hold bool) (*claudeRecoveryWriter, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	return &claudeRecoveryWriter{ResponseWriter: c.Writer, headers: http.Header{"X-Sub2api-Session-Id": {"UPSTREAM_UUID"}}, status: 200, size: -1, hold: hold, session: "CLIENT_UUID", exchange: &service.ClaudeRecoveryExchange{}}, rec
}
func TestRecoveryWriterDoesNotLeakUpstreamIDsAndHoldsNonStream(t *testing.T) {
	w, rec := recoveryWriterForTest(t, true)
	_, e := w.Write([]byte(`{"ok":true}`))
	require.NoError(t, e)
	require.Empty(t, rec.Body.String())
	require.True(t, w.Written())
	require.NoError(t, w.publish())
	require.Equal(t, "CLIENT_UUID", rec.Header().Get("X-Sub2API-Session-Id"))
	require.NotContains(t, rec.Header().Get("X-Sub2API-Session-Id"), "UPSTREAM")
}
func TestRecoveryWriterDoesNotTreatTextAsStreamCompletion(t *testing.T) {
	w, _ := recoveryWriterForTest(t, false)
	_, e := w.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"event: message_stop\"}}\n\n"))
	require.NoError(t, e)
	require.False(t, w.terminal)
	_, e = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	require.NoError(t, e)
	require.True(t, w.terminal)
}
func TestRecoveryWriterCommitAndLostLeaseBoundaries(t *testing.T) {
	w, rec := recoveryWriterForTest(t, false)
	w.Flush()
	require.True(t, w.Written())
	require.True(t, w.committed)
	w.WriteHeader(503)
	require.Equal(t, 200, w.Status())
	require.Equal(t, "CLIENT_UUID", rec.Header().Get("X-Sub2API-Session-Id"))
	w.exchange.LeaseLost.Store(true)
	_, e := w.Write([]byte("must not publish"))
	require.Error(t, e)
	require.NotContains(t, rec.Body.String(), "must not publish")
}
