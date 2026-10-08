package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// prepareNativeClaudeTransport selects only the version/platform actually
// captured. Custom destinations and explicitly bound profiles keep their policy.
func prepareNativeClaudeTransport(req *http.Request, c *gin.Context, account *Account, body []byte) {
	if req == nil || c == nil || c.Request == nil ||
		!preserveNativeClaudeRequest(req.Context(), c, account, body) ||
		account.GetTLSFingerprintProfileID() != 0 {
		return
	}
	if enabled, ok := account.Extra["claude_native_transport"].(bool); ok && !enabled {
		return
	}
	u := req.URL
	if u == nil || u.Scheme != "https" || u.User != nil ||
		!strings.EqualFold(u.Hostname(), "api.anthropic.com") || (u.Port() != "" && u.Port() != "443") {
		return
	}
	h := req.Header
	if ExtractCLIVersion(getHeaderRaw(h, "User-Agent")) != "2.1.292" ||
		(getHeaderRaw(h, "X-Stainless-OS") != "Linux" && getHeaderRaw(h, "X-Stainless-OS") != "MacOS") || getHeaderRaw(h, "X-Stainless-Arch") != "x64" ||
		getHeaderRaw(h, "X-Stainless-Package-Version") != "0.128.0" || getHeaderRaw(h, "X-Stainless-Runtime-Version") != "v26.3.0" {
		return
	}
	*req = *req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileClaude2292))
}

type nativeClaudeBodyDigestKey struct{}

// The extracted JS constructs a cch placeholder; its final runtime algorithm is
// not established. Preserve the original body instead of sending edited content
// with a stale opaque value or fabricating a replacement.
func withNativeClaudeBodyIntegrity(ctx context.Context, c *gin.Context, account *Account, body []byte) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if !preserveNativeClaudeRequest(ctx, c, account, body) {
		return ctx
	}
	if _, exists := ctx.Value(nativeClaudeBodyDigestKey{}).([sha256.Size]byte); exists {
		return ctx
	}
	hasChecksum := false
	gjson.GetBytes(body, "system").ForEach(func(_, block gjson.Result) bool {
		text := block.Get("text").String()
		if strings.HasPrefix(text, "x-anthropic-billing-header:") && strings.Contains(text, " cch=") {
			hasChecksum = true
		}
		return true
	})
	if !hasChecksum {
		return ctx
	}
	return context.WithValue(ctx, nativeClaudeBodyDigestKey{}, sha256.Sum256(body))
}

func validateNativeClaudeBodyIntegrity(ctx context.Context, c *gin.Context, body []byte) error {
	if ctx == nil {
		return nil
	}
	expected, exists := ctx.Value(nativeClaudeBodyDigestKey{}).([sha256.Size]byte)
	if !exists || expected == sha256.Sum256(body) {
		return nil
	}
	err := fmt.Errorf("native Claude request conflicts with a body-changing model or beta policy; adjust the caller request or explicitly disable claude_native_passthrough")
	if c != nil {
		c.JSON(http.StatusBadRequest, gin.H{"type": "error", "error": gin.H{"type": "invalid_request_error", "message": err.Error()}})
	}
	return err
}

// preserveNativeClaudeRequest is a serialization policy, not authentication.
// Account credentials, billing eligibility, beta policy and URL validation still
// apply. An explicit account override restores the legacy identity transforms.
func preserveNativeClaudeRequest(ctx context.Context, c *gin.Context, account *Account, body []byte) bool {
	if account == nil || account.Platform != PlatformAnthropic ||
		(!account.IsAnthropicOAuthOrSetupToken() && account.Type != AccountTypeAPIKey) {
		return false
	}
	if enabled, ok := account.Extra["claude_native_passthrough"].(bool); ok && !enabled {
		return false
	}
	if ctx != nil && IsClaudeCodeClient(ctx) {
		return true
	}
	metadata := gjson.GetBytes(body, "metadata.user_id").String()
	if c != nil && c.Request != nil && isClaudeCodeClient(c.GetHeader("User-Agent"), metadata) {
		return true
	}
	return metadata != "" && systemHasBillingAttributionBlock(body)
}
