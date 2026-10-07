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
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var claude2292ValidationFixtures = []string{
	"validation-baseline-01", "validation-retry-503-01", "validation-retry-503-02",
	"validation-multi-turn-01", "validation-multi-turn-02", "validation-oauth-synthetic-01",
	"validation-read-tool-01", "validation-read-tool-02", "validation-unicode-01",
	"validation-count-context-01", "validation-count-context-02", "validation-count-context-03",
}

// The production services run unchanged; only their persistence and network
// boundaries are replaced. Synthetic account credentials never leave this test.
type claude2292IdentityCache struct {
	stubIdentityCache
	maskedSession string
}

func (c *claude2292IdentityCache) GetMaskedSessionID(context.Context, int64) (string, error) {
	return c.maskedSession, nil
}

func (c *claude2292IdentityCache) SetMaskedSessionID(_ context.Context, _ int64, session string) error {
	c.maskedSession = session
	return nil
}

func forwardClaude2292Capture(t *testing.T, svc *GatewayService, account *Account, capture claudeCapturedRequest) (*http.Request, []byte) {
	t.Helper()
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
	var bodyMap map[string]any
	require.NoError(t, json.Unmarshal(capture.Body, &bodyMap))
	// Match the handler's validated client context, particularly count_tokens,
	// whose native requests do not contain metadata or a system prompt.
	validated := NewClaudeCodeValidator().Validate(c.Request, bodyMap)
	c.Request = c.Request.WithContext(SetClaudeCodeClient(c.Request.Context(), validated))
	countTokens := strings.Contains(capture.Path, "/count_tokens")
	if countTokens {
		require.True(t, validated)
	}
	responseBody := "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":32}}}\n\n" +
		"data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":1}}\n\n" +
		"data: {\"type\":\"message_stop\"}\n\n"
	responseType := "text/event-stream"
	if countTokens {
		responseBody, responseType = `{"input_tokens":128}`, "application/json"
	}
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {responseType}},
		Body: io.NopCloser(strings.NewReader(responseBody)),
	}}
	svc.httpUpstream = upstream
	if countTokens {
		err = svc.ForwardCountTokens(c.Request.Context(), c, account, parsed)
	} else {
		_, err = svc.Forward(c.Request.Context(), c, account, parsed)
	}
	require.NoError(t, err)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, c.Request.URL.Path, upstream.lastReq.URL.Path)
	require.Equal(t, "true", upstream.lastReq.URL.Query().Get("beta"))
	require.Empty(t, getHeaderRaw(upstream.lastReq.Header, "cookie"))
	if account.IsOAuth() {
		require.Equal(t, "Bearer upstream-only", getHeaderRaw(upstream.lastReq.Header, "authorization"))
		require.Empty(t, getHeaderRaw(upstream.lastReq.Header, "x-api-key"))
	} else {
		require.Equal(t, "upstream-only", getHeaderRaw(upstream.lastReq.Header, "x-api-key"))
		require.Empty(t, getHeaderRaw(upstream.lastReq.Header, "authorization"))
	}
	return upstream.lastReq, upstream.lastBody
}

func newClaude2292Gateway() *GatewayService {
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	return &GatewayService{cfg: cfg, responseHeaderFilter: compileResponseHeaderFilter(cfg),
		rateLimitService: &RateLimitService{}, deferredService: &DeferredService{}}
}

func newClaude2292Account(kind string) *Account {
	return &Account{ID: 292, Platform: PlatformAnthropic, Type: kind, Concurrency: 1,
		Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"api_key": "upstream-only", "access_token": "upstream-only"},
		Extra:       map[string]any{"anthropic_passthrough": true, "account_uuid": "11111111-1111-4111-8111-111111111111"}}
}

func TestClaudeCode2292ExtendedCaptureForwarding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, fixture := range claude2292ValidationFixtures {
		for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			t.Run(fixture+"/"+kind, func(t *testing.T) {
				capture := loadNativeClaudeCapture(t, "2_1_292", fixture)
				req, body := forwardClaude2292Capture(t, newClaude2292Gateway(), newClaude2292Account(kind), capture)
				require.Equal(t, []byte(capture.Body), body)
				for key, value := range capture.Headers {
					if strings.EqualFold(key, "anthropic-beta") {
						for _, beta := range strings.Split(value, ",") {
							require.True(t, containsBetaToken(getHeaderRaw(req.Header, key), beta), beta)
						}
						continue
					}
					require.Equal(t, value, getHeaderRaw(req.Header, key), key)
				}
			})
		}
	}
}

func TestClaudeCode2292OAuthIdentitySettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SUB2API_CLAUDE_CLI_VERSION", "2.1.258")
	for _, fixture := range claude2292ValidationFixtures {
		for _, policy := range []struct {
			name         string
			fingerprint  bool
			metadataPass bool
			mask         bool
			useDefaults  bool
		}{
			{"preserve", false, true, false, false},
			{"defaults", true, false, false, true},
			{"fingerprint_only", true, true, false, false},
			{"metadata_only", false, false, false, false},
			{"masked_session", true, false, true, false},
		} {
			for _, cacheMode := range []string{"cold", "older_version", "same_version_other_platform"} {
				t.Run(fixture+"/"+policy.name+"/"+cacheMode, func(t *testing.T) {
					resetGatewayForwardingSettingsCacheForTest(t)
					capture := loadNativeClaudeCapture(t, "2_1_292", fixture)
					values := map[string]string{}
					if !policy.useDefaults {
						values[SettingKeyEnableFingerprintUnification] = map[bool]string{true: "true", false: "false"}[policy.fingerprint]
						values[SettingKeyEnableMetadataPassthrough] = map[bool]string{true: "true", false: "false"}[policy.metadataPass]
					}
					cache := &claude2292IdentityCache{}
					if cacheMode != "cold" {
						fp := defaultFingerprint()
						fp.UserAgent = "claude-cli/2.1.258 (external, sdk-cli)"
						if cacheMode == "same_version_other_platform" {
							fp.UserAgent = "claude-cli/2.1.292 (external, sdk-cli)"
						}
						fp.ClientID, fp.UpdatedAt = strings.Repeat("c", 64), time.Now().Unix()
						cache.fingerprint = &fp
					}
					svc := newClaude2292Gateway()
					svc.settingService = NewSettingService(&gatewayTTLSettingRepo{data: values}, svc.cfg)
					svc.identityService = NewIdentityService(cache)
					account := newClaude2292Account(AccountTypeOAuth)
					account.Extra["session_id_masking_enabled"] = policy.mask
					var lastMetadata string
					for attempt := 0; attempt < 2; attempt++ {
						req, body := forwardClaude2292Capture(t, svc, account, capture)
						wantBody := []byte(capture.Body)
						originalUID := gjson.GetBytes(capture.Body, "metadata.user_id").String()
						actualUID := gjson.GetBytes(body, "metadata.user_id").String()
						if !policy.metadataPass && originalUID != "" {
							uid := ParseMetadataUserID(actualUID)
							require.NotNil(t, uid)
							require.Equal(t, account.GetExtraString("account_uuid"), uid.AccountUUID)
							require.Equal(t, cache.fingerprint.ClientID, uid.DeviceID)
							require.NotEqual(t, originalUID, actualUID)
							if policy.mask {
								require.Equal(t, cache.maskedSession, uid.SessionID)
							}
							if attempt > 0 {
								require.Equal(t, lastMetadata, actualUID, "session identity must be stable")
							}
							lastMetadata = actualUID
							var err error
							wantBody, err = sjson.SetBytes(wantBody, "metadata.user_id", actualUID)
							require.NoError(t, err)
						}
						require.Equal(t, wantBody, body, "identity settings must not rewrite unrelated native content or same-version attribution")
						if actualUID != "" {
							require.Equal(t, ParseMetadataUserID(actualUID).SessionID, getHeaderRaw(req.Header, "X-Claude-Code-Session-Id"))
						}
						wantSDK := capture.Headers["X-Stainless-Package-Version"]
						wantArch := capture.Headers["X-Stainless-Arch"]
						if policy.fingerprint && cacheMode == "same_version_other_platform" {
							wantSDK, wantArch = "0.94.0", "arm64"
						}
						require.Equal(t, wantSDK, getHeaderRaw(req.Header, "X-Stainless-Package-Version"))
						require.Equal(t, wantArch, getHeaderRaw(req.Header, "X-Stainless-Arch"))
						require.True(t, containsBetaToken(getHeaderRaw(req.Header, "anthropic-beta"), claude.BetaOAuth))
					}
				})
			}
		}
	}
}
