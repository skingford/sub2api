package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
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

// Keep the mutation guard for versions or destinations without a verified CCH
// implementation. The measured 2.1.292 first-party path is finalized below.
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

// finalizeNativeClaudeRequest runs after body policy and final header overrides.
// Only the measured native version is eligible; unknown versions retain the
// opaque-body guard. Authentication and entitlement policy remain independent.
func finalizeNativeClaudeRequest(req *http.Request, c *gin.Context, account *Account, body []byte) ([]byte, error) {
	u := req.URL
	billing := gjson.GetBytes(body, "system.0.text")
	known := preserveNativeClaudeRequest(req.Context(), c, account, body) &&
		req.Method == http.MethodPost && u != nil && u.Scheme == "https" && u.User == nil &&
		strings.EqualFold(u.Hostname(), "api.anthropic.com") && (u.Port() == "" || u.Port() == "443") &&
		(u.Path == "/v1/messages" || u.Path == "/v1/messages/count_tokens") &&
		getHeaderRaw(req.Header, "anthropic-version") != "" &&
		ExtractCLIVersion(getHeaderRaw(req.Header, "User-Agent")) == "2.1.292" &&
		strings.HasPrefix(billing.String(), "x-anthropic-billing-header: cc_version=2.1.292.")
	if !known {
		return body, validateNativeClaudeBodyIntegrity(req.Context(), c, body)
	}
	out, err := finalizeClaude2292Billing(body, billing)
	if err != nil {
		if c != nil {
			c.JSON(http.StatusBadRequest, gin.H{"type": "error", "error": gin.H{"type": "invalid_request_error", "message": err.Error()}})
		}
		return nil, err
	}
	if !bytes.Equal(out, body) {
		// Keep retries, Content-Length, debug snapshots and the actual wire body
		// on the same final bytes. The closure owns an immutable snapshot.
		snapshot := bytes.Clone(out)
		req.Body = io.NopCloser(bytes.NewReader(snapshot))
		req.ContentLength = int64(len(snapshot))
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(snapshot)), nil }
		deleteHeaderAllForms(req.Header, "content-length")
	}
	return out, nil
}

func finalizeClaude2292Billing(body []byte, billing gjson.Result) ([]byte, error) {
	badLayout := func() ([]byte, error) {
		return nil, fmt.Errorf("native Claude 2.1.292 billing layout is outside the verified CCH format")
	}
	if !gjson.ValidBytes(body) || billing.Type != gjson.String || billing.Index <= 0 {
		return badLayout()
	}
	// Work on raw billing bytes so messages, tools, signatures, JSON key order,
	// whitespace and Unicode encodings are not reserialized.
	raw := billing.Raw
	var out []byte
	if at := strings.Index(raw, " cch="); at >= 0 {
		start := at + len(" cch=")
		if start+5 >= len(raw) || raw[start+5] != ';' {
			return badLayout()
		}
		for _, ch := range raw[start : start+5] {
			if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
				return badLayout()
			}
		}
		out = bytes.Clone(body)
		copy(out[billing.Index+start:], "00000")
	} else {
		at := strings.Index(raw, " cc_entrypoint=")
		if at < 0 {
			return badLayout()
		}
		end := strings.IndexByte(raw[at:], ';')
		if end < 0 {
			return badLayout()
		}
		at += billing.Index + end + 1
		out = make([]byte, 0, len(body)+len(" cch=00000;"))
		out = append(out, body[:at]...)
		out = append(out, " cch=00000;"...)
		out = append(out, body[at:]...)
	}
	// The runtime replaces the first sentinel in the system window. Reject an
	// ambiguous/escaped layout rather than patching a user-controlled text block.
	system := bytes.Index(out, []byte(`"system":[`))
	if system < 0 {
		return badLayout()
	}
	first := bytes.Index(out[system:min(system+300, len(out))], []byte("cch=00000"))
	newBilling := gjson.GetBytes(out, "system.0.text")
	position := newBilling.Index + strings.Index(newBilling.Raw, " cch=00000;") + 1
	if first < 0 || system+first != position {
		return badLayout()
	}
	final, ok := claude.CCH2292(out)
	if !ok {
		return badLayout()
	}
	return final, nil
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
