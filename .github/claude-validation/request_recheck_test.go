//go:build unit

// Load with go -overlay into internal/service. Native captures are read only;
// production Forward runs with the existing persistence and HTTP test ports.
package service

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	requestbody "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNativeRequestRecheck(t *testing.T) {
	root, output := os.Getenv("CLAUDE_EXTENDED_AUDIT_INPUT"), os.Getenv("CLAUDE_REQUEST_RECHECK_OUTPUT")
	if root == "" || output == "" {
		t.Skip("requires isolated captures and an output path")
	}
	gin.SetMode(gin.TestMode)
	files, err := filepath.Glob(filepath.Join(root, "*", "requests.json"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	var rows []map[string]any
	for _, file := range files {
		var records []struct {
			Method  string            `json:"method"`
			Path    string            `json:"path"`
			Headers map[string]string `json:"headers"`
			Raw     string            `json:"raw_body_utf8"`
			Wire    string            `json:"wire_body_base64"`
		}
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(data, &records))
		for index, record := range records {
			if !strings.HasPrefix(record.Path, "/v1/messages") {
				continue
			}
			body := []byte(record.Raw)
			var cchEqual *bool
			billing := gjson.GetBytes(body, "system.0.text")
			if strings.Contains(billing.String(), " cch=") {
				calculated, err := finalizeClaude2292Billing(body, billing)
				require.NoError(t, err)
				equal := bytes.Equal(body, calculated)
				cchEqual = &equal
			}
			for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
				for _, passthrough := range []bool{false, true} {
					svc, up := newClaudeContractGateway(t)
					account := newClaude2292Account(kind)
					account.Extra["anthropic_passthrough"] = passthrough
					response := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(response)
					wire := body
					if record.Wire != "" {
						wire, err = base64.StdEncoding.DecodeString(record.Wire)
						require.NoError(t, err)
					}
					c.Request = httptest.NewRequest(record.Method, record.Path, bytes.NewReader(wire))
					for key, value := range record.Headers {
						c.Request.Header.Set(key, value)
					}
					decoded, err := requestbody.ReadRequestBodyWithPrealloc(c.Request)
					require.NoError(t, err)
					require.Equal(t, body, decoded, "production ingress decoding differs from captured logical JSON")
					var bodyMap map[string]any
					require.NoError(t, json.Unmarshal(body, &bodyMap))
					// Match the real handler's validated context. Native count_tokens
					// has neither metadata nor a billing system prompt to infer from.
					validated := NewClaudeCodeValidator().Validate(c.Request, bodyMap)
					ctx := SetClaudeCodeClient(c.Request.Context(), validated)
					c.Request = c.Request.WithContext(ctx)
					parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
					require.NoError(t, err)
					var forwardErr error
					if strings.Contains(record.Path, "/count_tokens") {
						forwardErr = svc.ForwardCountTokens(ctx, c, account, parsed)
					} else {
						_, forwardErr = svc.Forward(ctx, c, account, parsed)
					}
					outboundLogical := up.body
					if up.request != nil && strings.EqualFold(getHeaderRaw(up.request.Header, "Content-Encoding"), "gzip") {
						r, err := gzip.NewReader(bytes.NewReader(up.body))
						require.NoError(t, err)
						outboundLogical, err = io.ReadAll(r)
						require.NoError(t, err)
						require.NoError(t, r.Close())
					}
					row := map[string]any{
						"case": filepath.Base(filepath.Dir(file)), "index": index,
						"account": kind, "passthrough": passthrough, "status": response.Code,
						"calls": up.calls, "body_equal": bytes.Equal(body, outboundLogical),
						"wire_body_equal": bytes.Equal(wire, up.body),
						"input_sha256":    recoveryHash(body), "output_sha256": recoveryHash(up.body),
						"native_cch_recomputed_equal": cchEqual,
						"handler_validated_native":    validated,
						"wire_compressed":             !bytes.Equal(wire, body),
						"ingress_decoded_equal":       bytes.Equal(decoded, body),
					}
					if forwardErr != nil {
						row["error"] = forwardErr.Error()
					}
					if req := up.request; req != nil {
						row["method_equal"] = req.Method == record.Method
						row["path_query_equal"] = req.URL.RequestURI() == record.Path
						row["target"] = req.URL.String()
						row["content_length_equal"] = req.ContentLength == int64(len(up.body))
						row["profile"] = HTTPUpstreamProfileFromContext(req.Context())
						if req.GetBody != nil {
							replay, err := req.GetBody()
							require.NoError(t, err)
							replayBody, err := io.ReadAll(replay)
							require.NoError(t, err)
							require.NoError(t, replay.Close())
							row["get_body_equal"] = bytes.Equal(up.body, replayBody)
						} else {
							row["get_body_equal"] = false
						}
						sourceHeaders := map[string]string{}
						for key, value := range record.Headers {
							sourceHeaders[strings.ToLower(key)] = value
						}
						row["same_auth_kind"] = (sourceHeaders["authorization"] != "") == (kind == AccountTypeOAuth)
						row["auth_kind_correct"] = (kind == AccountTypeOAuth && getHeaderRaw(req.Header, "authorization") != "" && getHeaderRaw(req.Header, "x-api-key") == "") ||
							(kind == AccountTypeAPIKey && getHeaderRaw(req.Header, "x-api-key") != "" && getHeaderRaw(req.Header, "authorization") == "")
						keys := map[string]bool{}
						for key := range sourceHeaders {
							keys[key] = true
						}
						for key := range req.Header {
							keys[strings.ToLower(key)] = true
						}
						var differences []map[string]string
						ordered := make([]string, 0, len(keys))
						for key := range keys {
							ordered = append(ordered, key)
						}
						sort.Strings(ordered)
						for _, key := range ordered {
							// Credentials change at the gateway; transport-owned headers
							// are covered by separate actual-wire tests, not this hook.
							switch key {
							case "authorization", "x-api-key", "host", "connection", "content-length":
								continue
							}
							actual := getHeaderRaw(req.Header, key)
							if sourceHeaders[key] != actual {
								differences = append(differences, map[string]string{"header": key, "native": sourceHeaders[key], "forwarded": actual})
							}
						}
						row["header_differences"] = differences
					}
					rows = append(rows, row)
				}
			}
		}
	}
	require.NotEmpty(t, rows)
	data, err := json.MarshalIndent(rows, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(output, append(data, '\n'), 0600))
}
