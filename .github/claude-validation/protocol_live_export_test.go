//go:build unit

package service

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Export synthetic requests through production Forward, with test persistence
// and a recording upstream. The separate live probe owns real authentication.
func TestClaudeProtocolLiveExport(t *testing.T) {
	folder := os.Getenv("CLAUDE_LIVE_REQUEST_EXPORT")
	if folder == "" {
		t.Skip("requires explicit isolated request export")
	}
	require.NoError(t, os.MkdirAll(folder, 0700))
	for _, pass := range []bool{false, true} {
		svc, up := newClaudeContractGateway(t)
		account := newClaude2292Account(AccountTypeAPIKey)
		account.Extra["anthropic_passthrough"] = pass
		account.Credentials["base_url"] = "https://relay.example"
		body := []byte(`{"model":"claude-haiku-4-5-20251001","max_tokens":64,"thinking":{"type":"disabled"},"messages":[{"role":"user","content":"Reply with exactly OK."}]}`)
		_, _, err := callClaudeContract(t, svc, account, body, nil, "/v1/messages")
		require.NoError(t, err)
		require.Equal(t, 1, up.calls)
		headers := map[string]string{}
		for key, values := range up.request.Header {
			if !strings.EqualFold(key, "x-api-key") && !strings.EqualFold(key, "authorization") {
				headers[key] = strings.Join(values, ", ")
			}
		}
		data, err := json.Marshal(map[string]any{"path": up.request.URL.RequestURI(), "headers": headers, "wire_body_base64": base64.StdEncoding.EncodeToString(up.body)})
		require.NoError(t, err)
		name := map[bool]string{false: "normal", true: "passthrough"}[pass]
		require.NoError(t, os.WriteFile(filepath.Join(folder, name+".json"), data, 0600))
	}
}
