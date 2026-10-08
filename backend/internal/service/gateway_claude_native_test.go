//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/sjson"
)

func TestClaudeNativeFingerprintMatchesExtracted2292Function(t *testing.T) {
	raw, err := os.ReadFile("testdata/claude_code_2_1_292/fingerprint-vectors.json")
	require.NoError(t, err)
	var fixture struct {
		Vectors []struct{ Text, Version, Expected string }
	}
	require.NoError(t, json.Unmarshal(raw, &fixture))
	require.Len(t, fixture.Vectors, 10)
	for _, v := range fixture.Vectors {
		t.Run(v.Expected, func(t *testing.T) { require.Equal(t, v.Expected, computeClaudeCodeFingerprintText(v.Text, v.Version)) })
	}
}

func TestClaudeNativeDefaultsPreserveIdentityAndCachedVersion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, fixture := range claude2292ValidationFixtures {
		t.Run(fixture, func(t *testing.T) {
			resetGatewayForwardingSettingsCacheForTest(t)
			capture := loadNativeClaudeCapture(t, "2_1_292", fixture)
			cache := &claude2292IdentityCache{stubIdentityCache: stubIdentityCache{fingerprint: &Fingerprint{
				ClientID: strings.Repeat("c", 64), UserAgent: "claude-cli/2.9.0 (external, cli)",
				StainlessPackageVersion: "0.94.0", StainlessOS: "MacOS", StainlessArch: "arm64",
				StainlessRuntimeVersion: "v24.3.0", UpdatedAt: time.Now().Unix(),
			}}}
			before := *cache.fingerprint
			svc := newClaude2292Gateway()
			svc.identityService = NewIdentityService(cache)
			svc.settingService = NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{
				SettingKeyEnableFingerprintUnification: "true", SettingKeyEnableMetadataPassthrough: "false",
				SettingKeyEnableAnthropicCacheTTL1hInjection: "true", SettingKeyEnableClientDatelineNormalization: "true",
			}}, svc.cfg)
			account := newClaude2292Account(AccountTypeOAuth)
			account.Extra["session_id_masking_enabled"] = true
			for attempt := 0; attempt < 2; attempt++ {
				req, body := forwardClaude2292Capture(t, svc, account, capture)
				require.Equal(t, []byte(capture.Body), body)
				require.Equal(t, before, *cache.fingerprint)
				require.Zero(t, cache.setCalls)
				require.Empty(t, cache.maskedSession)
				for key, value := range capture.Headers {
					if strings.EqualFold(key, "anthropic-beta") {
						for _, beta := range strings.Split(value, ",") {
							require.True(t, containsBetaToken(getHeaderRaw(req.Header, key), beta))
						}
						require.True(t, containsBetaToken(getHeaderRaw(req.Header, key), claude.BetaOAuth))
					} else {
						require.Equal(t, value, getHeaderRaw(req.Header, key), key)
					}
				}
				require.Equal(t, HTTPUpstreamProfileClaude2292, HTTPUpstreamProfileFromContext(req.Context()))
			}
		})
	}
}

func TestClaudeNativeTransportSelectionIsBounded(t *testing.T) {
	capture := loadNativeClaudeCapture(t, "2_1_292", "validation-baseline-01")
	for _, tc := range []struct {
		name, platform, arch, version, target string
		disabled, bound, want                 bool
	}{
		{name: "linux", platform: "Linux", arch: "x64", version: "2.1.292", want: true},
		{name: "macos", platform: "MacOS", arch: "x64", version: "2.1.292", want: true},
		{name: "unknown_version", platform: "Linux", arch: "x64", version: "2.1.293"},
		{name: "unknown_arch", platform: "Linux", arch: "arm64", version: "2.1.292"},
		{name: "custom_origin", platform: "Linux", arch: "x64", version: "2.1.292", target: "https://relay.invalid/v1/messages"},
		{name: "explicit_transport_opt_out", platform: "Linux", arch: "x64", version: "2.1.292", disabled: true},
		{name: "explicit_profile", platform: "Linux", arch: "x64", version: "2.1.292", bound: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, capture.Path, nil)
			for k, v := range capture.Headers {
				c.Request.Header.Set(k, v)
			}
			c.Request.Header.Set("User-Agent", "claude-cli/"+tc.version+" (external, sdk-cli)")
			c.Request.Header.Set("X-Stainless-OS", tc.platform)
			c.Request.Header.Set("X-Stainless-Arch", tc.arch)
			target := tc.target
			if target == "" {
				target = "https://api.anthropic.com/v1/messages?beta=true"
			}
			req := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(capture.Body))
			req.Header = c.Request.Header.Clone()
			account := newClaude2292Account(AccountTypeOAuth)
			if tc.disabled {
				account.Extra["claude_native_transport"] = false
			}
			if tc.bound {
				account.Extra["tls_fingerprint_profile_id"] = int64(42)
			}
			prepareNativeClaudeTransport(req, c, account, capture.Body)
			require.Equal(t, tc.want, HTTPUpstreamProfileFromContext(req.Context()) == HTTPUpstreamProfileClaude2292)
		})
	}
}

func TestClaudeNativeRejectsBodyEditsWithOpaqueAttribution(t *testing.T) {
	capture := loadNativeClaudeCapture(t, "2_1_292", "validation-baseline-01")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, capture.Path, bytes.NewReader(capture.Body))
	for k, v := range capture.Headers {
		c.Request.Header.Set(k, v)
	}
	c.Set(betaPolicyFilterSetKey, map[string]struct{}{claude.BetaCacheDiagnosis: {}})
	account := newClaude2292Account(AccountTypeOAuth)
	_, _, err := newClaude2292Gateway().buildUpstreamRequest(context.Background(), c, account, capture.Body, "synthetic-token", "oauth", "claude-sonnet-4-6", true, false)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, err.Error(), "native Claude request conflicts")
}

func TestClaudeNativePreservesOpaqueThinkingPayloads(t *testing.T) {
	// These values are synthetic opaque blobs, not valid provider signatures.
	capture := loadNativeClaudeCapture(t, "2_1_292", "validation-baseline-01")
	var err error
	capture.Body, err = sjson.SetBytes(capture.Body, "system.0.text", "x-anthropic-billing-header: cc_version=2.1.292.357; cc_entrypoint=sdk-cli;")
	require.NoError(t, err)
	capture.Body, err = sjson.SetRawBytes(capture.Body, "messages.1", []byte(`{"role":"assistant","content":[{"type":"thinking","thinking":"synthetic","signature":"opaque-synthetic-signature"},{"type":"redacted_thinking","data":"opaque-synthetic-redacted-data"}]}`))
	require.NoError(t, err)
	capture.Body, err = sjson.SetRawBytes(capture.Body, "messages.2", []byte(`{"role":"user","content":"continue"}`))
	require.NoError(t, err)
	capture.Body, err = sjson.DeleteBytes(capture.Body, "thinking")
	require.NoError(t, err)
	_, out := forwardClaude2292Capture(t, newClaude2292Gateway(), newClaude2292Account(AccountTypeOAuth), capture)
	require.Equal(t, []byte(capture.Body), out, "native opaque content must not be stripped or recomputed")
}
