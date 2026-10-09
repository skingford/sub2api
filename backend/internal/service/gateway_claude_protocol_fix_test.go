//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestClaudeProtocolGzipPolicyCCH(t *testing.T) {
	for _, fixture := range []string{"parameter_alignment/native-gzip.json", "gzip_alignment/gzip-blocks2-2.json"} {
		for _, pass := range []bool{false, true} {
			for _, policy := range []string{"ua-newer", "ua-custom", "native-off", "model-map"} {
				t.Run(fixture+"/"+map[bool]string{false: "normal", true: "passthrough"}[pass]+"/"+policy, func(t *testing.T) {
					c, body, wire := nativeGzipFixtureRequest(t, "testdata/claude_code_2_1_292/"+fixture)
					svc, _ := newClaudeContractGateway(t)
					up := &claudeGzipWireUpstream{}
					svc.httpUpstream = up
					a := newClaude2292Account(AccountTypeAPIKey)
					a.Extra["anthropic_passthrough"] = pass
					if strings.HasPrefix(policy, "ua-") {
						ua := "claude-cli/2.1.293 (external, cli)"
						if policy == "ua-custom" {
							ua = "local-synthetic-client/1.0"
						}
						a.Credentials[credKeyHeaderOverrideEnabled] = true
						a.Credentials[credKeyHeaderOverrides] = map[string]any{"user-agent": ua}
					}
					if policy == "native-off" {
						a.Extra["claude_native_passthrough"] = false
					}
					if policy == "model-map" {
						a.Credentials["model_mapping"] = map[string]any{"claude-sonnet-4-6": "claude-opus-4-6"}
					}
					parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
					require.NoError(t, err)
					_, err = svc.Forward(c.Request.Context(), c, a, parsed)
					require.NoError(t, err)
					require.Equal(t, 1, up.calls)
					if strings.HasPrefix(policy, "ua-") {
						require.Equal(t, wire, up.wire)
						require.Equal(t, "gzip", getHeaderRaw(up.request.Header, "Content-Encoding"))
						require.Equal(t, HTTPUpstreamProfileDefault, HTTPUpstreamProfileFromContext(up.request.Context()))
						require.False(t, claude.GzipUsesApplicationHeader2292(up.request.Context()))
					} else {
						require.Empty(t, getHeaderRaw(up.request.Header, "Content-Encoding"))
						// Independent native-runtime answers from CC-20261009-008 policy oracle.
						expected := "90fb0"
						if strings.Contains(fixture, "blocks") {
							expected = "47671"
						}
						require.Contains(t, gjson.GetBytes(up.wire, "system.0.text").String(), "cch="+expected+";")
					}
					require.Equal(t, int64(len(up.wire)), up.request.ContentLength)
					replay, err := up.request.GetBody()
					require.NoError(t, err)
					data, err := io.ReadAll(replay)
					require.NoError(t, err)
					require.NoError(t, replay.Close())
					require.Equal(t, up.wire, data)
				})
			}
		}
	}
}

func TestClaudeProtocolUnknownChecksumCannotTranscode(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		for _, edit := range []bool{false, true} {
			t.Run(map[bool]string{false: "preserve", true: "legacy"}[disabled]+"/"+map[bool]string{false: "same", true: "changed"}[edit], func(t *testing.T) {
				c, body, wire := nativeGzipRequest(t)
				a := newClaude2292Account(AccountTypeAPIKey)
				a.Extra["claude_native_passthrough"] = !disabled
				ctx := withNativeClaudeBodyIntegrity(c.Request.Context(), c, a, body)
				if edit {
					var err error
					body, err = sjson.SetBytes(body, "messages.0.content", "Synthetic changed message")
					require.NoError(t, err)
				}
				req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
				require.NoError(t, err)
				req.Header = c.Request.Header.Clone()
				req.Header.Set("User-Agent", "claude-cli/9.9.9 (external, cli)")
				out, err := finalizeNativeClaudeRequest(req, c, a, body)
				if disabled || edit {
					require.Error(t, err)
					require.Nil(t, out)
					require.Equal(t, 400, c.Writer.Status())
					return
				}
				require.NoError(t, err)
				actual, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.Equal(t, wire, actual)
			})
		}
	}
}

func TestClaudeProtocolErrorSignals(t *testing.T) {
	cases := []struct {
		name, body string
		recover    bool
	}{
		{"standard", `{"type":"error","error":{"type":"invalid_request_error","message":"The request body is not valid JSON"}}`, true},
		{"escaped-message-growth", `{"type":"error","error":{"type":"invalid_request_error","message":"The request body is not valid JSON ` + strings.Repeat("<", 2000) + `"}}`, true},
		{"escaped-type-growth", `{"type":"error","error":{"type":"` + strings.Repeat("<", 2000) + `","message":"The request body is not valid JSON"}}`, true},
		{"large-number", `{"type":"error","error":{"type":"invalid_request_error","message":"The request body is not valid JSON"},"extra":1e999}`, true},
		{"alternate", `{"error":"bad json: unexpected EOF","reason":"bad_json"}`, true},
		{"plaintext", "bad json: unexpected EOF\n", true},
		{"invalid-char", "bad json: invalid character secret-token https://example.invalid/?api_key=private", true},
		{"quoted-string", `"bad json: unexpected EOF"`, false},
		{"wrong-reason", `{"error":"bad json: unexpected EOF","reason":"other"}`, false},
		{"json-no-trim", `{"error":"bad json: unexpected EOF\n","reason":"bad_json"}`, false},
		{"leading-space", " bad json: unexpected EOF", false},
		{"leading-standard", `{"type":"error","error":{"type":"invalid_request_error","message":" The request body is not valid JSON"}}`, false},
		{"nested-message", `{"type":"error","error":{"type":"api_error","message":"{\"error\":{\"message\":\"The request body is not valid JSON\"}}"}}`, false},
		{"wrong-type", `{"type":"other","error":{"type":"api_error","message":"The request body is not valid JSON"}}`, false},
		{"numeric-error-type", `{"type":"error","error":{"type":3,"message":"The request body is not valid JSON"}}`, false},
		{"ordinary", `{"type":"error","error":{"type":"invalid_request_error","message":"Unknown model"}}`, false},
		{"bom", "\xef\xbb\xbfbad json: EOF\ufeff", true},
		{"not-js-space", "bad json: EOF\u0085", false},
		{"huge-json", `{"type":"error","error":{"type":"invalid_request_error","message":"The request body is not valid JSON ` + strings.Repeat("x", 8192) + `"}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.recover, claudeInvalidJSONRejection([]byte(tc.body)))
			svc, _ := newClaudeContractGateway(t)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			resp := &http.Response{StatusCode: 400, Header: http.Header{"Request-Id": []string{"req_local"}}, Body: io.NopCloser(strings.NewReader(tc.body))}
			require.Error(t, svc.returnClaudeUpstreamError(context.Background(), c, newClaude2292Account(AccountTypeAPIKey), resp, "claude-sonnet-4-6"))
			require.Equal(t, 400, rec.Code)
			require.Equal(t, "req_local", rec.Header().Get("request-id"))
			require.Equal(t, tc.recover, claudeInvalidJSONRejection(rec.Body.Bytes()), "normalization must preserve positive AND negative retry decisions")
			require.NotContains(t, rec.Body.String(), "secret-token")
			require.NotContains(t, rec.Body.String(), "private")
		})
	}
	for _, value := range []string{"local-ray", ""} {
		t.Run("cf-ray/"+value, func(t *testing.T) {
			svc, _ := newClaudeContractGateway(t)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			h := http.Header{"cF-rAy": []string{value}, "Retry-After": []string{"5"}, "X-Should-Retry": []string{"false"}}
			body := `{"type":"error","error":{"type":"permission_error","message":"Local refusal https://example.invalid/?key=private"}}`
			resp := &http.Response{StatusCode: 403, Header: h, Body: io.NopCloser(strings.NewReader(body))}
			require.Error(t, svc.returnClaudeUpstreamError(context.Background(), c, newClaude2292Account(AccountTypeAPIKey), resp, "claude-sonnet-4-6"))
			require.Equal(t, []string{value}, rec.Header()["Cf-Ray"])
			require.Equal(t, "5", rec.Header().Get("Retry-After"))
			require.Equal(t, "false", rec.Header().Get("X-Should-Retry"))
			require.Equal(t, 403, rec.Code)
			require.NotContains(t, rec.Body.String(), "private")
			require.Equal(t, "permission_error", gjson.Get(rec.Body.String(), "error.type").String())
			h["cF-rAy"][0] = "mutated"
			require.Equal(t, []string{value}, rec.Header()["Cf-Ray"])
		})
	}
	// JSON duplicate keys use native JSON.parse's last-key semantics.
	duplicate := `{"error":"bad json: EOF","reason":"other","reason":"bad_json"}`
	require.True(t, claudeInvalidJSONRejection([]byte(duplicate)))
	var decoded any
	require.NoError(t, json.Unmarshal([]byte(duplicate), &decoded))
}
