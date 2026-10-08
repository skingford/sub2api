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
	if claude.EffectiveCLIVersion() != claudeCompatibilityVersion {
		return ctx, claudeCompatibilityError(c, "unsupported Claude compatibility version; select the verified 2.1.292 profile")
	}
	if c != nil {
		if value, exists := c.Get(claudeCompatibilityGinKey); exists {
			if state, ok := value.(*claudeCompatibilityState); ok {
				return context.WithValue(ctx, claudeCompatibilityKey{}, state), nil
			}
		}
	}
	session := ""
	if metadata := ParseMetadataUserID(gjson.GetBytes(body, "metadata.user_id").String()); metadata != nil {
		session = metadata.SessionID
	}
	if c != nil && c.Request != nil {
		for _, key := range []string{claudeConversationHeader, "X-Claude-Code-Session-Id"} {
			value := strings.TrimSpace(c.GetHeader(key))
			if value == "" {
				continue
			}
			if session != "" && !strings.EqualFold(session, value) {
				return ctx, claudeCompatibilityError(c, "conflicting Claude conversation identifiers")
			}
			session = value
		}
	}
	if session != "" {
		parsed, err := uuid.Parse(session)
		if err != nil || parsed == uuid.Nil {
			return ctx, claudeCompatibilityError(c, "Claude conversation identifier must be a non-zero UUID")
		}
		session = parsed.String()
	} else {
		session = uuid.NewString()
	}
	state := &claudeCompatibilityState{Version: claudeCompatibilityVersion, SessionID: session, PromptID: uuid.NewString()}
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
		state.Resumed = c.GetHeader(claudeConversationHeader) != ""
		c.Set(claudeCompatibilityGinKey, state)
		c.Header(claudeConversationHeader, session)
		exposed := c.Writer.Header().Get("Access-Control-Expose-Headers")
		if exposed == "" {
			c.Header("Access-Control-Expose-Headers", claudeConversationHeader)
		} else if !strings.Contains(strings.ToLower(exposed), strings.ToLower(claudeConversationHeader)) {
			c.Header("Access-Control-Expose-Headers", exposed+", "+claudeConversationHeader)
		}
	}
	return context.WithValue(ctx, claudeCompatibilityKey{}, state), nil
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
	setOpsUpstreamError(c, resp.StatusCode, message, "")
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform: account.Platform, AccountID: account.ID, UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID: resp.Header.Get("request-id"), Kind: "http_error", Message: message,
	})
	for _, key := range []string{"request-id", "retry-after", "retry-after-ms", "x-should-retry"} {
		if value := resp.Header.Get(key); value != "" {
			c.Header(key, value)
		}
	}
	MarkResponseCommitted(c)
	errorType := gjson.GetBytes(body, "error.type").String()
	if errorType == "" {
		errorType = "api_error"
	}
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
