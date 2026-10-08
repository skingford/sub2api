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
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const capturedCacheDiagnosisBeta = "cache-diagnosis-2026-04-07"

func loadClaude2291Capture(t *testing.T, name string) claudeCapturedRequest {
	t.Helper()
	return loadNativeClaudeCapture(t, "2_1_291", name)
}

func loadNativeClaudeCapture(t *testing.T, version, name string) claudeCapturedRequest {
	t.Helper()
	dir := filepath.Join("testdata", "claude_code_"+version)
	data, err := os.ReadFile(filepath.Join(dir, name+".request.json"))
	require.NoError(t, err)
	var capture claudeCapturedRequest
	require.NoError(t, json.Unmarshal(data, &capture))
	wireBody, err := os.ReadFile(filepath.Join(dir, name+".body.json"))
	require.NoError(t, err)
	require.JSONEq(t, string(capture.Body), string(wireBody))
	capture.Body = wireBody
	return capture
}

func TestClaudeCode2291CaptureForwarding(t *testing.T) {
	testNativeClaudeCaptureForwarding(t, "2_1_291")
}

func TestClaudeCode2292CaptureForwarding(t *testing.T) {
	testNativeClaudeCaptureForwarding(t, "2_1_292")
}

func testNativeClaudeCaptureForwarding(t *testing.T, version string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	for _, fixture := range []string{"linux-firstparty", "macos-loopback"} {
		for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			t.Run(fixture+"/"+kind, func(t *testing.T) {
				capture := loadNativeClaudeCapture(t, version, fixture)
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
					Body: io.NopCloser(strings.NewReader("data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":32}}}\n\n" +
						"data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":1}}\n\n" +
						"data: {\"type\":\"message_stop\"}\n\n")),
				}}
				cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
				svc := &GatewayService{cfg: cfg, httpUpstream: upstream,
					responseHeaderFilter: compileResponseHeaderFilter(cfg),
					rateLimitService:     &RateLimitService{}, deferredService: &DeferredService{}}
				account := &Account{ID: 291, Platform: PlatformAnthropic, Type: kind,
					Concurrency: 1, Status: StatusActive, Schedulable: true,
					Credentials: map[string]any{"api_key": "upstream-only", "access_token": "upstream-only"},
					Extra:       map[string]any{"anthropic_passthrough": true}}
				_, err = svc.Forward(context.Background(), c, account, parsed)
				require.NoError(t, err)
				require.NotNil(t, upstream.lastReq)
				expectedBody := []byte(capture.Body)
				if version == "2_1_292" && fixture == "macos-loopback" {
					// A custom-base CLI omits CCH. The first-party outbound request
					// restores it; this golden output comes from the native runtime.
					expectedBody, err = os.ReadFile("testdata/claude_code_2_1_292/cch-macos-forward.body.json")
					require.NoError(t, err)
				}
				require.Equal(t, expectedBody, upstream.lastBody, "preserve native fields and finalize first-party attribution")
				for key, value := range capture.Headers {
					if strings.EqualFold(key, "anthropic-beta") && kind == AccountTypeOAuth {
						value = strings.Replace(value, claude.BetaClaudeCode, claude.BetaClaudeCode+","+claude.BetaOAuth, 1)
					}
					require.Equal(t, value, getHeaderRaw(upstream.lastReq.Header, key), key)
				}
				require.Empty(t, getHeaderRaw(upstream.lastReq.Header, "cookie"))
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

func TestClaudeCode2291DiagnosticsCapability(t *testing.T) {
	capture := loadClaude2291Capture(t, "linux-firstparty")
	beta := capture.Headers["anthropic-beta"]
	out, changed := sanitizeAnthropicBodyForBetaTokens(capture.Body, beta)
	require.False(t, changed)
	require.Equal(t, []byte(capture.Body), out)
	require.Equal(t, gjson.Null, gjson.GetBytes(out, "diagnostics.previous_message_id").Type)

	withoutDiagnosticBeta := stripBetaTokens(beta, []string{capturedCacheDiagnosisBeta})
	out, changed = sanitizeAnthropicBodyForBetaTokens(capture.Body, withoutDiagnosticBeta)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(out, "diagnostics").Exists())
	want, err := sjson.DeleteBytes(capture.Body, "diagnostics")
	require.NoError(t, err)
	require.Equal(t, want, out)
	again, changed := sanitizeAnthropicBodyForBetaTokens(out, withoutDiagnosticBeta)
	require.False(t, changed)
	require.Equal(t, out, again)

	localCapture := loadClaude2291Capture(t, "macos-loopback")
	out, changed = sanitizeAnthropicBodyForBetaTokens(localCapture.Body, beta)
	require.False(t, changed)
	require.Equal(t, []byte(localCapture.Body), out, "enabling a beta must not manufacture diagnostics")
}

func TestClaudeCode2291DiagnosticsFinalBeta(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, route := range []string{"apikey_passthrough", "apikey", "oauth", "oauth_mimic"} {
		for _, enabled := range []bool{false, true} {
			t.Run(route+"/"+map[bool]string{false: "filtered", true: "enabled"}[enabled], func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				c.Request.Header.Set("anthropic-beta", claude.BetaClaudeCode+","+capturedCacheDiagnosisBeta)
				account := newAnthropicAPIKeyAccountForTest()
				tokenType := "apikey"
				if strings.HasPrefix(route, "oauth") {
					account.Type, tokenType = AccountTypeOAuth, "oauth"
					if !enabled {
						c.Set(betaPolicyFilterSetKey, map[string]struct{}{capturedCacheDiagnosisBeta: {}})
					}
				} else if !enabled {
					account.Credentials[credKeyHeaderOverrideEnabled] = true
					account.Credentials[credKeyHeaderOverrides] = map[string]any{"anthropic-beta": claude.BetaClaudeCode}
				}
				body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}],"diagnostics":{"previous_message_id":"msg_synthetic_previous"}}`)
				svc := &GatewayService{cfg: &config.Config{}}
				var req *http.Request
				var out []byte
				var err error
				if route == "apikey_passthrough" {
					req, out, err = svc.buildUpstreamRequestAnthropicAPIKeyPassthrough(context.Background(), c, account, body, "synthetic-key")
				} else {
					req, out, err = svc.buildUpstreamRequest(context.Background(), c, account, body, "synthetic-key", tokenType, "claude-sonnet-4-6", false, route == "oauth_mimic")
				}
				require.NoError(t, err)
				require.Equal(t, enabled, containsBetaToken(getHeaderRaw(req.Header, "anthropic-beta"), capturedCacheDiagnosisBeta))
				require.Equal(t, enabled, gjson.GetBytes(out, "diagnostics").Exists())
				if enabled {
					require.Equal(t, "msg_synthetic_previous", gjson.GetBytes(out, "diagnostics.previous_message_id").String())
				}
			})
		}
	}
}
