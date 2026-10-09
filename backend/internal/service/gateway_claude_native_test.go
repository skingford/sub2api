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
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
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

func TestClaudeNativeFinalizesCCHAfterBetaPolicy(t *testing.T) {
	capture := loadNativeClaudeCapture(t, "2_1_292", "validation-baseline-01")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, capture.Path, bytes.NewReader(capture.Body))
	for k, v := range capture.Headers {
		c.Request.Header.Set(k, v)
	}
	c.Set(betaPolicyFilterSetKey, map[string]struct{}{claude.BetaCacheDiagnosis: {}})
	account := newClaude2292Account(AccountTypeOAuth)
	req, body, err := newClaude2292Gateway().buildUpstreamRequest(context.Background(), c, account, capture.Body, "synthetic-token", "oauth", "claude-sonnet-4-6", true, false)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(body, "diagnostics").Exists())
	require.NotEqual(t, gjson.GetBytes(capture.Body, "system.0.text").String(), gjson.GetBytes(body, "system.0.text").String())
	wire, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Equal(t, body, wire)
	require.Equal(t, int64(len(body)), req.ContentLength)
	retry, err := req.GetBody()
	require.NoError(t, err)
	defer retry.Close()
	wire, err = io.ReadAll(retry)
	require.NoError(t, err)
	require.Equal(t, body, wire)
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
	require.Equal(t, gjson.GetBytes(capture.Body, "messages").Raw, gjson.GetBytes(out, "messages").Raw, "native opaque content must not be stripped or recomputed")
	require.Contains(t, gjson.GetBytes(out, "system.0.text").String(), " cch=")
}

func TestClaudeNativeCCHScopeAndUnknownVersionGuard(t *testing.T) {
	for _, tc := range []struct {
		name, version, target     string
		edit, disabled, wantError bool
	}{
		{name: "known", version: "2.1.292", edit: true},
		{name: "unknown_unchanged", version: "2.1.293"},
		{name: "unknown_modified", version: "2.1.293", edit: true, wantError: true},
		{name: "unknown_modified_legacy", version: "2.1.293", edit: true, disabled: true, wantError: true},
		{name: "custom_unchanged", version: "2.1.292", target: "https://relay.invalid/v1/messages"},
		{name: "custom_modified", version: "2.1.292", target: "https://relay.invalid/v1/messages", edit: true, wantError: true},
		{name: "explicit_legacy", version: "2.1.292", edit: true, disabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := loadNativeClaudeCapture(t, "2_1_292", "validation-baseline-01")
			original := bytes.ReplaceAll(capture.Body, []byte("2.1.292"), []byte(tc.version))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			c.Request.Header.Set("User-Agent", "claude-cli/"+tc.version+" (external, cli)")
			account := newClaude2292Account(AccountTypeOAuth)
			account.Extra["claude_native_passthrough"] = !tc.disabled
			ctx := withNativeClaudeBodyIntegrity(context.Background(), c, account, original)
			body := original
			if tc.edit {
				var err error
				body, err = sjson.SetBytes(body, "messages.0.content", "local changed content")
				require.NoError(t, err)
			}
			target := tc.target
			if target == "" {
				target = "https://api.anthropic.com/v1/messages"
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
			require.NoError(t, err)
			req.Header = c.Request.Header.Clone()
			req.Header.Set("anthropic-version", "2023-06-01")
			out, err := finalizeNativeClaudeRequest(req, c, account, body)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tc.name == "known" || tc.name == "explicit_legacy" {
				require.NotEqual(t, body, out)
			} else {
				require.Equal(t, body, out)
			}
		})
	}
}

func TestClaudeNativeCCHRestoresPlaceholderAndRejectsAmbiguousLayout(t *testing.T) {
	capture := loadNativeClaudeCapture(t, "2_1_292", "validation-baseline-01")
	for _, mode := range []string{"missing", "placeholder", "stale", "unsupported_spacing", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			body := bytes.Clone(capture.Body)
			switch mode {
			case "missing":
				body = bytes.Replace(body, []byte(" cch=ab5ce;"), nil, 1)
			case "placeholder":
				body = bytes.Replace(body, []byte("cch=ab5ce"), []byte("cch=00000"), 1)
			case "stale":
				body = bytes.Replace(body, []byte("cch=ab5ce"), []byte("cch=fffff"), 1)
			case "unsupported_spacing":
				body = bytes.Replace(body, []byte(`"system":[`), []byte(`"system": [`), 1)
			case "malformed":
				body = bytes.Replace(body, []byte("cch=ab5ce"), []byte("cch=invalid"), 1)
			}
			out, err := finalizeClaude2292Billing(body, gjson.GetBytes(body, "system.0.text"))
			if mode == "unsupported_spacing" || mode == "malformed" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, []byte(capture.Body), out, "must restore the independently captured native checksum")
		})
	}
}

func TestClaudeNativeCCHAllRequestBuildersUseFinalBody(t *testing.T) {
	for _, route := range []string{"messages_api", "messages_oauth", "messages_passthrough", "count_api", "count_oauth", "count_passthrough"} {
		t.Run(route, func(t *testing.T) {
			capture := loadNativeClaudeCapture(t, "2_1_292", "validation-baseline-01")
			body := bytes.Replace(capture.Body, []byte("cch=ab5ce"), []byte("cch=fffff"), 1)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, capture.Path, bytes.NewReader(body))
			for k, v := range capture.Headers {
				c.Request.Header.Set(k, v)
			}
			kind, tokenType := AccountTypeAPIKey, "apikey"
			if strings.HasSuffix(route, "oauth") {
				kind, tokenType = AccountTypeOAuth, "oauth"
			}
			account := newClaude2292Account(kind)
			svc := newClaude2292Gateway()
			var req *http.Request
			var err error
			switch route {
			case "messages_passthrough":
				req, _, err = svc.buildUpstreamRequestAnthropicAPIKeyPassthrough(context.Background(), c, account, body, "synthetic-key")
			case "count_passthrough":
				req, err = svc.buildCountTokensRequestAnthropicAPIKeyPassthrough(context.Background(), c, account, body, "synthetic-key")
			case "count_api", "count_oauth":
				req, _, err = svc.buildCountTokensRequest(context.Background(), c, account, body, "synthetic-token", tokenType, "claude-sonnet-4-6", false)
			default:
				req, _, err = svc.buildUpstreamRequest(context.Background(), c, account, body, "synthetic-token", tokenType, "claude-sonnet-4-6", true, false)
			}
			require.NoError(t, err)
			actual, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			billing := gjson.GetBytes(actual, "system.0.text")
			zero := bytes.Clone(actual)
			at := billing.Index + strings.Index(billing.Raw, "cch=") + 4
			require.Greater(t, at, billing.Index+3)
			copy(zero[at:at+5], "00000")
			want, ok := claude.CCH2292(zero)
			require.True(t, ok)
			require.Equal(t, want, actual, "final request must pass the independently verified checksum routine")
			require.Equal(t, int64(len(actual)), req.ContentLength)
			if strings.HasPrefix(route, "count_") {
				require.False(t, gjson.GetBytes(actual, "stream").Exists())
			}
		})
	}
}
