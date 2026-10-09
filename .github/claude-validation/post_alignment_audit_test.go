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
	"github.com/tidwall/gjson"
)

// Observe configuration combinations without treating a mock failure as an
// actual provider rejection. No production implementations are replaced.
func TestPostAlignmentEncodingPolicyAudit(t *testing.T) {
	output := os.Getenv("CLAUDE_ENCODING_POLICY_AUDIT")
	if output == "" {
		t.Skip("requires audit output path")
	}
	var rows []map[string]any
	for _, mapping := range []bool{false, true} {
		for _, override := range []string{"", "gzip", "identity"} {
			c, body, wire := nativeGzipRequest(t)
			svc, _ := newClaudeContractGateway(t)
			up := &claudeGzipWireUpstream{}
			svc.httpUpstream = up
			account := newClaude2292Account(AccountTypeAPIKey)
			if mapping {
				account.Credentials["model_mapping"] = map[string]any{"claude-sonnet-4-6": "claude-opus-4-6"}
			}
			if override != "" {
				account.Credentials[credKeyHeaderOverrideEnabled] = true
				account.Credentials[credKeyHeaderOverrides] = map[string]any{"content-encoding": override}
				require.NotContains(t, account.GetHeaderOverrides(), "content-encoding", "old stored overrides must be filtered")
			}
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
			require.NoError(t, err)
			_, err = svc.Forward(c.Request.Context(), c, account, parsed)
			row := map[string]any{"mapping": mapping, "encoding_override": override, "calls": up.calls, "status": c.Writer.Status(), "wire_preserved": recoveryHash(wire) == recoveryHash(up.wire)}
			if err != nil {
				row["error"] = err.Error()
			}
			if up.request != nil {
				variants := map[string][]string{}
				for key, values := range up.request.Header {
					if strings.EqualFold(key, "content-encoding") {
						variants[key] = values
					}
				}
				row["encoding_header_variants"] = variants
				row["content_encoding"] = getHeaderRaw(up.request.Header, "Content-Encoding")
				row["wire_is_json"] = gjson.ValidBytes(up.wire)
				row["wire_model"] = gjson.GetBytes(up.wire, "model").String()
				row["content_length_correct"] = up.request.ContentLength == int64(len(up.wire))
				if folder := os.Getenv("CLAUDE_ENCODING_POLICY_EXPORT"); folder != "" {
					require.NoError(t, os.MkdirAll(folder, 0700))
					headers := map[string]string{}
					for k, v := range up.request.Header {
						headers[k] = v[0]
					}
					name := map[bool]string{false: "unchanged", true: "mapped"}[mapping] + "-" + map[string]string{"": "none", "gzip": "gzip", "identity": "identity"}[override]
					value := map[string]any{"method": up.request.Method, "path": up.request.URL.RequestURI(), "headers": headers, "raw_body_utf8": string(body), "wire_body_base64": base64.StdEncoding.EncodeToString(up.wire), "synthetic_auth": "apikey", "upstream_profile": HTTPUpstreamProfileFromContext(up.request.Context())}
					b, e := json.MarshalIndent(value, "", "  ")
					require.NoError(t, e)
					require.NoError(t, os.WriteFile(filepath.Join(folder, name+".json"), append(b, '\n'), 0600))
				}
			}
			rows = append(rows, row)
		}
	}
	data, err := json.MarshalIndent(rows, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(output, append(data, '\n'), 0600))
}
