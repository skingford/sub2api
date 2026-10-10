package service

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const claudeCompatibilityVersion = "2.1.292"
const claudeConversationHeader = "X-Sub2API-Session-Id"
const claudeCompatibilityGinKey = "claudeCompatibilityState"

func verifiedClaudeCompatibilityVersion(version string) bool {
	return claude.IsVerifiedCLIVersion(version)
}

// Haiku 5.5's measured defaults belong to 2.1.295. Keep other models on the
// request's frozen configured profile; never use an unverified version.
func claudeCompatibilityModelVersion(version, model string) string {
	if version == "2.1.292" && claude.IsHaiku55(model) {
		return "2.1.295"
	}
	return version
}

type claudeCompatibilityKey struct{}
type nativeClaudeOriginKey struct{}

type claudeCompatibilityState struct {
	Version   string
	SessionID string
	PromptID  string
	APIKeyID  int64
	GroupID   int64
	Resumed   bool
}

func claudeCompatibilityFromContext(ctx context.Context) *claudeCompatibilityState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(claudeCompatibilityKey{}).(*claudeCompatibilityState)
	return state
}

// prepareClaudeCompatibility fixes the origin classification before any
// generated metadata/billing can resemble a native request. Session continuity
// is explicit: echo the returned UUID on later turns; identical opening text is
// not a conversation identifier.
func prepareClaudeCompatibility(ctx context.Context, c *gin.Context, body []byte, requests ...*ParsedRequest) (context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, nativeClaudeOriginKey{}, false)
	if state := claudeCompatibilityFromContext(ctx); state != nil {
		return ctx, nil
	}
	if c != nil {
		if value, exists := c.Get(claudeCompatibilityGinKey); exists {
			if state, ok := value.(*claudeCompatibilityState); ok && state != nil {
				return context.WithValue(ctx, claudeCompatibilityKey{}, state), nil
			}
		}
	}
	version := claude.EffectiveCLIVersion()
	if !verifiedClaudeCompatibilityVersion(version) {
		return ctx, claudeCompatibilityError(c, "unsupported Claude compatibility version; select a verified "+strings.Join(claude.VerifiedCLIVersions(), " or ")+" profile")
	}
	// Use the routing validator's canonical UUID comparison and inspect every
	// header value before publishing a conversation for the caller to resume.
	session, err := claudeRequestSession(ctx, c, body)
	if err != nil {
		return ctx, claudeCompatibilityError(c, err.Error())
	}
	if session == "" {
		session = uuid.NewString()
	}
	version = claudeCompatibilityModelVersion(version, gjson.GetBytes(body, "model").String())
	state := &claudeCompatibilityState{Version: version, SessionID: session, PromptID: uuid.NewString()}
	if c != nil {
		if prompt := strings.TrimSpace(c.GetHeader("x-claude-code-prompt-id")); prompt != "" {
			id, err := uuid.Parse(prompt)
			if err != nil || id == uuid.Nil {
				return ctx, claudeCompatibilityError(c, "Claude prompt identifier must be a non-zero UUID")
			}
			state.PromptID = id.String()
		}
	}
	if len(requests) > 0 && requests[0] != nil {
		state.GroupID = derefGroupID(requests[0].GroupID)
		if scope := requests[0].SessionContext; scope != nil {
			state.APIKeyID = scope.APIKeyID
		}
	}
	if c != nil {
		state.Resumed = hasClaudeConversationHeader(c)
		c.Set(claudeCompatibilityGinKey, state)
		c.Header(claudeConversationHeader, session)
		exposeClaudeResponseHeader(c.Writer.Header(), claudeConversationHeader)
	}
	return context.WithValue(ctx, claudeCompatibilityKey{}, state), nil
}

// Compare complete tokens across all field values, preserving middleware's
// existing CORS declarations. A similarly named header does not expose this one.
func exposeClaudeResponseHeader(headers http.Header, name string) {
	for _, value := range headers.Values("Access-Control-Expose-Headers") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), name) {
				return
			}
		}
	}
	headers.Add("Access-Control-Expose-Headers", name)
}

func claudeConversationRoutingKey(apiKeyID int64, session string) string {
	return "claude-conversation:" + strconv.FormatInt(apiKeyID, 10) + ":" + session
}

func claudeCompatibilityError(c *gin.Context, message string) error {
	return claudeCompatibilityStatusError(c, http.StatusBadRequest, message)
}

func claudeCompatibilityStatusError(c *gin.Context, status int, message string) error {
	err := fmt.Errorf("%s", message)
	if c != nil && !c.Writer.Written() {
		errorType := "invalid_request_error"
		if status >= 500 {
			errorType = "api_error"
		}
		c.JSON(status, gin.H{"type": "error", "error": gin.H{"type": errorType, "message": message}})
	}
	return err
}

func applyClaudeCompatibilityContext(req *http.Request, countTokens bool) {
	state := claudeCompatibilityFromContext(req.Context())
	if state == nil {
		return
	}
	setHeaderRaw(req.Header, "X-Claude-Code-Session-Id", state.SessionID)
	if countTokens {
		setHeaderRaw(req.Header, "x-claude-code-request-class", "auxiliary")
		deleteHeaderAllForms(req.Header, "X-Stainless-Timeout")
		deleteHeaderAllForms(req.Header, "x-claude-code-prompt-id")
	} else {
		setHeaderRaw(req.Header, "x-claude-code-request-class", "main")
		setHeaderRaw(req.Header, "x-claude-code-prompt-id", state.PromptID)
	}
}

// Native callers own their retry controller. A single gateway operation must
// not silently retry a refusal or rotate credentials underneath that caller.
// Compatibility callers also receive one upstream outcome per API operation;
// they may explicitly retry using the returned conversation identifier.
func claudeCallerOwnsRetries(ctx context.Context, c *gin.Context, account *Account, body []byte) bool {
	return account != nil && account.Platform == PlatformAnthropic &&
		(preserveNativeClaudeRequest(ctx, c, account, body) || claudeCompatibilityFromContext(ctx) != nil || ClaudeRecoveryFromContext(ctx) != nil)
}

func (s *GatewayService) returnClaudeUpstreamError(ctx context.Context, c *gin.Context, account *Account, resp *http.Response, model string) error {
	body, err := s.readUpstreamErrorBody(resp)
	if err != nil {
		return fmt.Errorf("read Claude upstream error: %w", err)
	}
	if s.rateLimitService != nil {
		// Retain accounting/cooldown bookkeeping, without another attempt.
		s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, model)
	}
	message := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	if message == "" {
		message = "Claude upstream rejected the request"
	}
	errorType := gjson.GetBytes(body, "error.type").String()
	if errorType == "" {
		errorType = "api_error"
	}
	if resp.StatusCode == http.StatusBadRequest {
		if claudeInvalidJSONRejection(body) {
			// Preserve the native recovery category without reflecting arbitrary
			// plaintext, parser snippets or nested JSON back to the caller. Keep
			// the complete response within the CLI's 8192-byte probe even when
			// JSON escaping would expand the upstream message or error type.
			message = claudeInvalidJSONMessage
			errorType = "invalid_request_error"
		} else if claudeInvalidJSONMessagePrefix(message) {
			// Trimming, nested-message extraction or a malformed original schema
			// must not invent a native compression-retry signal.
			message = "Upstream error: " + message
		}
	}
	setOpsUpstreamError(c, resp.StatusCode, message, "")
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform: account.Platform, AccountID: account.ID, UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID: resp.Header.Get("request-id"), Kind: "http_error", Message: message,
	})
	for _, key := range []string{"request-id", "retry-after", "retry-after-ms", "x-should-retry"} {
		if value := resp.Header.Get(key); value != "" {
			c.Header(key, value)
			exposeClaudeResponseHeader(c.Writer.Header(), key)
		}
	}
	// Native compression fallback uses presence (including an empty value) of
	// cf-ray to distinguish an origin refusal from an intermediary's rejection.
	for key, values := range resp.Header {
		if strings.EqualFold(key, "cf-ray") && len(values) > 0 {
			deleteHeaderAllForms(c.Writer.Header(), "cf-ray")
			c.Writer.Header()["Cf-Ray"] = append([]string(nil), values...)
			exposeClaudeResponseHeader(c.Writer.Header(), "cf-ray")
			break
		}
	}
	MarkResponseCommitted(c)
	// Preserve status/retry signals while retaining gateway error redaction.
	c.JSON(resp.StatusCode, gin.H{"type": "error", "error": gin.H{"type": errorType, "message": message}})
	return fmt.Errorf("claude upstream returned HTTP %d", resp.StatusCode)
}

// A known mismatch must not be sent with another account's credential. Empty
// account identifiers (including token-only CLI sessions) are not inferred.
func validateClaudeAccountIdentity(ctx context.Context, c *gin.Context, account *Account, body []byte) error {
	if account == nil || !account.IsAnthropicOAuthOrSetupToken() ||
		(!preserveNativeClaudeRequest(ctx, c, account, body) && claudeCompatibilityFromContext(ctx) == nil) {
		return nil
	}
	metadata := ParseMetadataUserID(gjson.GetBytes(body, "metadata.user_id").String())
	selected := strings.TrimSpace(account.GetExtraString("account_uuid"))
	if metadata != nil && metadata.AccountUUID != "" && selected != "" && !strings.EqualFold(metadata.AccountUUID, selected) {
		return claudeCompatibilityError(c, "Claude account identity does not match the selected upstream account; use the same account or start a new conversation")
	}
	return nil
}
