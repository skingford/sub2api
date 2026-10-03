//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type claudeCapturedRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
}

func loadClaude2286Capture(t *testing.T, name string) claudeCapturedRequest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "claude_code_2_1_286", name+".request.json"))
	require.NoError(t, err)
	var capture claudeCapturedRequest
	require.NoError(t, json.Unmarshal(data, &capture))
	return capture
}

func TestClaudeCode2286CaptureForwarding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, fixture := range []string{
		"01-minimal-auto", "02-read-first", "03-read-result",
		"04-minimal-default", "05-retry-first", "06-retry-second",
	} {
		for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			t.Run(fixture+"/"+kind, func(t *testing.T) {
				capture := loadClaude2286Capture(t, fixture)
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(capture.Method, capture.Path, bytes.NewReader(capture.Body))
				for key, value := range capture.Headers {
					c.Request.Header.Set(key, value)
				}
				c.Request.Header.Set("Authorization", "Bearer downstream-only")
				c.Request.Header.Set("X-Api-Key", "downstream-only")
				c.Request.Header.Set("Cookie", "session=downstream-only")
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(capture.Body), PlatformAnthropic)
				require.NoError(t, err)
				upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"text/event-stream"}},
					Body: io.NopCloser(strings.NewReader("data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":64}}}\n\n" +
						"data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":8}}\n\n" +
						"data: {\"type\":\"message_stop\"}\n\n")),
				}}
				cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
				svc := &GatewayService{
					cfg: cfg, responseHeaderFilter: compileResponseHeaderFilter(cfg),
					httpUpstream: upstream, rateLimitService: &RateLimitService{}, deferredService: &DeferredService{},
				}
				account := &Account{
					ID: 286, Platform: PlatformAnthropic, Type: kind, Concurrency: 1,
					Status: StatusActive, Schedulable: true,
					Credentials: map[string]any{"api_key": "upstream-only", "access_token": "upstream-only"},
					Extra:       map[string]any{"anthropic_passthrough": true},
				}
				_, err = svc.Forward(context.Background(), c, account, parsed)
				require.NoError(t, err)
				require.NotNil(t, upstream.lastReq)
				require.Equal(t, "/v1/messages", upstream.lastReq.URL.Path)
				require.Equal(t, "true", upstream.lastReq.URL.Query().Get("beta"))
				require.JSONEq(t, string(capture.Body), string(upstream.lastBody), "native client body must survive forwarding")
				for key, value := range capture.Headers {
					if strings.EqualFold(key, "anthropic-beta") {
						for _, beta := range strings.Split(value, ",") {
							require.True(t, anthropicBetaTokensContains(getHeaderRaw(upstream.lastReq.Header, key), beta), beta)
						}
						continue
					}
					require.Equal(t, value, getHeaderRaw(upstream.lastReq.Header, key), key)
				}
				require.Empty(t, getHeaderRaw(upstream.lastReq.Header, "cookie"))
				require.Empty(t, getHeaderRaw(upstream.lastReq.Header, "x-stainless-helper-method"))
				require.Empty(t, getHeaderRaw(upstream.lastReq.Header, "x-client-request-id"))
				if kind == AccountTypeOAuth {
					require.Equal(t, "Bearer upstream-only", getHeaderRaw(upstream.lastReq.Header, "authorization"))
					require.Empty(t, getHeaderRaw(upstream.lastReq.Header, "x-api-key"))
				} else {
					require.Equal(t, "upstream-only", getHeaderRaw(upstream.lastReq.Header, "x-api-key"))
					require.Empty(t, getHeaderRaw(upstream.lastReq.Header, "authorization"))
				}
			})
		}
	}
}

func TestClaudeCode2286NormalizationPreservesThinkingTemperatureOmission(t *testing.T) {
	for _, thinking := range []string{"adaptive", "enabled", "between_tools"} {
		t.Run(thinking, func(t *testing.T) {
			body := []byte(`{"model":"claude-sonnet-5-5","tools":[],"messages":[],"max_tokens":128000,"thinking":{"type":"` + thinking + `"}}`)
			out, _ := normalizeClaudeOAuthRequestBody(body, "claude-sonnet-5-5", claudeOAuthNormalizeOptions{})
			require.False(t, gjson.GetBytes(out, "temperature").Exists())
		})
	}
}

func TestClaudeCode2286MimicDoesNotInventOptionalHeaders(t *testing.T) {
	for _, stream := range []bool{false, true} {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		applyClaudeCodeMimicHeaders(req, stream, "claude-cli/2.1.286 (external, sdk-cli)")
		require.Empty(t, getHeaderRaw(req.Header, "x-stainless-helper-method"))
		require.Empty(t, getHeaderRaw(req.Header, "x-client-request-id"))
		require.Equal(t, "application/json", getHeaderRaw(req.Header, "Accept"))
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("x-client-request-id", "caller-request-id")
	req.Header.Set("x-stainless-helper-method", "caller-helper")
	applyClaudeCodeMimicHeaders(req, true, "claude-cli/2.1.286 (external, sdk-cli)")
	require.Equal(t, "caller-request-id", getHeaderRaw(req.Header, "x-client-request-id"))
	require.Equal(t, "caller-helper", getHeaderRaw(req.Header, "x-stainless-helper-method"))
}

func TestClaudeCode2286BetaCapabilityFiltering(t *testing.T) {
	capture := loadClaude2286Capture(t, "01-minimal-auto")
	beta := capture.Headers["anthropic-beta"]
	for _, tc := range []struct {
		name       string
		drop       string
		wantOutput bool
		wantGuard  bool
	}{
		{"all_capabilities", "", true, true},
		{"without_per_turn_control", "per-turn-control-2026-07-01", false, true},
		{"without_system_messages", "mid-conversation-system-2026-04-07", false, true},
		{"without_classifier", "dangerous-tool-use-2026-09-03", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			final := stripBetaTokens(beta, []string{tc.drop})
			out, _ := sanitizeAnthropicBodyForBetaTokens(capture.Body, final)
			require.Equal(t, tc.wantOutput, gjson.GetBytes(out, "messages.1.output_config").Exists())
			require.Equal(t, tc.wantGuard, gjson.GetBytes(out, "safeguards").Exists())
			require.Equal(t, "medium", gjson.GetBytes(out, "output_config.effort").String())
			require.Equal(t, gjson.GetBytes(capture.Body, "metadata.user_id").String(), gjson.GetBytes(out, "metadata.user_id").String())
			again, changed := sanitizeAnthropicBodyForBetaTokens(out, final)
			require.False(t, changed)
			require.Equal(t, out, again)
		})
	}
	defaultCapture := loadClaude2286Capture(t, "04-minimal-default")
	out, _ := sanitizeAnthropicBodyForBetaTokens(defaultCapture.Body, beta)
	require.False(t, gjson.GetBytes(out, "safeguards").Exists(), "advertising a capability must not manufacture context")
}

func TestClaudeCode2286ConditionalHeadersForwarded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	capture := loadClaude2286Capture(t, "04-minimal-default")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	for key, value := range capture.Headers {
		c.Request.Header.Set(key, value)
	}
	headers := map[string]string{
		"x-client-app":                      "synthetic-sdk",
		"x-claude-code-agent-id":            "synthetic-agent",
		"x-claude-code-parent-agent-id":     "synthetic-parent",
		"x-claude-code-agent-type":          "subagent",
		"x-claude-code-request-class":       "main",
		"x-claude-code-prompt-id":           "synthetic-prompt",
		"x-claude-code-context-compacted":   "true",
		"x-claude-code-compaction":          "auto",
		"x-claude-code-prev-tool-durations": "12",
		"traceparent":                       "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	}
	for key, value := range headers {
		c.Request.Header.Set(key, value)
	}
	c.Request.Header.Set("x-unlisted-private-header", "must-not-leak")
	svc := &GatewayService{cfg: &config.Config{}}
	account := newAnthropicAPIKeyAccountForTest()
	req, _, err := svc.buildUpstreamRequestAnthropicAPIKeyPassthrough(context.Background(), c, account, capture.Body, "upstream-only")
	require.NoError(t, err)
	for key, value := range headers {
		require.Equal(t, value, getHeaderRaw(req.Header, key), key)
	}
	require.Empty(t, getHeaderRaw(req.Header, "x-unlisted-private-header"))
}

func TestClaudeCode2286NormalizationPreservesExplicitTemperature(t *testing.T) {
	for _, thinking := range []string{"adaptive", "enabled", "disabled"} {
		body := []byte(`{"model":"claude-sonnet-4-6","thinking":{"type":"` + thinking + `"},"temperature":0.7}`)
		out, _ := normalizeClaudeOAuthRequestBody(body, "claude-sonnet-4-6", claudeOAuthNormalizeOptions{})
		require.Equal(t, 0.7, gjson.GetBytes(out, "temperature").Float())
	}
	for _, thinking := range []string{``, `,"thinking":{"type":"disabled"}`} {
		body := []byte(`{"model":"claude-sonnet-4-6"` + thinking + `}`)
		out, _ := normalizeClaudeOAuthRequestBody(body, "claude-sonnet-4-6", claudeOAuthNormalizeOptions{})
		require.Equal(t, float64(1), gjson.GetBytes(out, "temperature").Float())
	}
}

func TestClaudeCode2286SafeguardsRejectMissingCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	capture := loadClaude2286Capture(t, "01-minimal-auto")
	for _, route := range []string{"apikey_passthrough", "apikey", "oauth"} {
		for _, source := range []string{"client", "override_or_policy"} {
			t.Run(route+"/"+source, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				for key, value := range capture.Headers {
					c.Request.Header.Set(key, value)
				}
				filtered := stripBetaTokens(capture.Headers["anthropic-beta"], []string{"dangerous-tool-use-2026-09-03"})
				account := newAnthropicAPIKeyAccountForTest()
				if route == "oauth" {
					account.Type = AccountTypeOAuth
				}
				if source == "client" {
					c.Request.Header.Set("anthropic-beta", filtered)
				} else if route == "oauth" {
					c.Set(betaPolicyFilterSetKey, map[string]struct{}{"dangerous-tool-use-2026-09-03": {}})
				} else {
					account.Credentials[credKeyHeaderOverrideEnabled] = true
					account.Credentials[credKeyHeaderOverrides] = map[string]any{"anthropic-beta": filtered}
				}
				svc := &GatewayService{cfg: &config.Config{}}
				var req *http.Request
				var err error
				if route == "apikey_passthrough" {
					req, _, err = svc.buildUpstreamRequestAnthropicAPIKeyPassthrough(context.Background(), c, account, capture.Body, "upstream-only")
				} else {
					tokenType := "apikey"
					if route == "oauth" {
						tokenType = "oauth"
					}
					req, _, err = svc.buildUpstreamRequest(context.Background(), c, account, capture.Body, "upstream-only", tokenType, "claude-sonnet-5-5", true, false)
				}
				require.ErrorContains(t, err, "safeguards requires anthropic-beta")
				require.Nil(t, req, "a classifier request must not be downgraded and sent")
				require.Equal(t, http.StatusBadRequest, rec.Code)
				require.Equal(t, "invalid_request_error", gjson.Get(rec.Body.String(), "error.type").String())
			})
		}
	}
}
