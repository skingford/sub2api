package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// ClaudeSessionStore keeps immutable ownership in the primary database. There
// is no expiry or reassignment operation. Implementations must serialize claims
// across processes and return the existing winner on conflict.
type ClaudeSessionStore interface {
	GetClaudeSessionAccountID(context.Context, string) (int64, error)
	ClaimClaudeSessionAccountID(context.Context, string, int64) (int64, error)
}

type claudeSessionOwnerKey struct{}
type claudeOriginalSessionKey struct{}

// ValidateClaudeSessionRouting rejects contradictory identifiers and unknown
// gateway-issued resume IDs before scheduling can reserve a new owner.
// When supplied, the parsed request receives the canonical routing identifier
// so handlers do not re-read just the first raw header value after validation.
func (s *GatewayService) ValidateClaudeSessionRouting(ctx context.Context, c *gin.Context, body []byte, requests ...*ParsedRequest) error {
	session, err := claudeRequestSession(ctx, c, body)
	if err != nil {
		return claudeCompatibilityError(c, err.Error())
	}
	if len(requests) > 0 && requests[0] != nil {
		requests[0].ClaudeSessionID = session
	}
	if session == "" || !hasClaudeConversationHeader(c) {
		return nil
	}
	if s.claudeSessionStore == nil {
		return claudeCompatibilityStatusError(c, http.StatusServiceUnavailable, "Claude conversation store is unavailable")
	}
	owner, err := s.claudeSessionStore.GetClaudeSessionAccountID(ctx, session)
	if err != nil {
		return claudeCompatibilityStatusError(c, http.StatusServiceUnavailable, "Claude conversation state is unavailable; retry later")
	}
	if owner == 0 {
		return claudeCompatibilityError(c, "Claude conversation is unknown; start a new conversation")
	}
	return nil
}

func hasClaudeConversationHeader(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	for name, values := range c.Request.Header {
		if strings.EqualFold(name, claudeConversationHeader) {
			for _, value := range values {
				if strings.TrimSpace(value) != "" {
					return true
				}
			}
		}
	}
	return false
}

func claudeSessionOwner(ctx context.Context) int64 {
	id, _ := ctx.Value(claudeSessionOwnerKey{}).(int64)
	return id
}

func claudeSessionFromRoutingKey(key string) string {
	if strings.HasPrefix(key, "claude-conversation:") {
		parts := strings.Split(key, ":")
		if len(parts) != 3 {
			return ""
		}
		key = parts[2]
	}
	id, err := uuid.Parse(key)
	if err != nil || id == uuid.Nil {
		return ""
	}
	return id.String()
}

// Use the same UUID across API keys, groups and endpoint adapters. Changing a
// routing scope never grants permission to move an existing conversation.
func (s *GatewayService) withClaudeSessionOwner(ctx context.Context, sessionHash string) (context.Context, error) {
	session := claudeSessionFromRoutingKey(sessionHash)
	if session == "" {
		return ctx, nil
	}
	if s.claudeSessionStore == nil {
		return ctx, fmt.Errorf("claude conversation store is unavailable")
	}
	owner, err := s.claudeSessionStore.GetClaudeSessionAccountID(ctx, session)
	if err != nil {
		return ctx, fmt.Errorf("read Claude conversation ownership: %w", err)
	}
	return context.WithValue(ctx, claudeSessionOwnerKey{}, owner), nil
}

func filterClaudeSessionOwner(ctx context.Context, accounts []Account) []Account {
	owner := claudeSessionOwner(ctx)
	if owner == 0 {
		return accounts
	}
	for i := range accounts {
		if accounts[i].ID == owner {
			return accounts[i : i+1]
		}
	}
	return nil
}

// Resolve all supplied identifiers, including native metadata, before any
// network send. Conflicting identifiers must not provide an alternate binding.
func claudeRequestSession(ctx context.Context, c *gin.Context, body []byte, headers ...http.Header) (string, error) {
	values := []string{}
	if original, ok := ctx.Value(claudeOriginalSessionKey{}).(string); ok {
		values = append(values, original)
	}
	for _, header := range headers {
		for name, supplied := range header {
			if strings.EqualFold(name, "X-Claude-Code-Session-Id") {
				values = append(values, supplied...)
			}
		}
	}
	if state := claudeCompatibilityFromContext(ctx); state != nil {
		values = append(values, state.SessionID)
	}
	if metadata := ParseMetadataUserID(gjson.GetBytes(body, "metadata.user_id").String()); metadata != nil {
		values = append(values, metadata.SessionID)
	}
	if c != nil && c.Request != nil {
		for name, supplied := range c.Request.Header {
			if strings.EqualFold(name, claudeConversationHeader) || strings.EqualFold(name, "X-Claude-Code-Session-Id") {
				values = append(values, supplied...)
			}
		}
	}
	session := ""
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		id, err := uuid.Parse(strings.TrimSpace(value))
		if err != nil || id == uuid.Nil {
			return "", fmt.Errorf("claude conversation identifier must be a non-zero UUID")
		}
		if session != "" && session != id.String() {
			return "", fmt.Errorf("conflicting Claude conversation identifiers")
		}
		session = id.String()
	}
	return session, nil
}

func (s *GatewayService) bindClaudeConversation(ctx context.Context, c *gin.Context, account *Account, body []byte, headers ...http.Header) error {
	if account == nil || account.Platform != PlatformAnthropic {
		return nil
	}
	session, err := claudeRequestSession(ctx, c, body, headers...)
	if err == nil {
		if p := ClaudeRecoveryFromContext(ctx); p != nil {
			if session != p.Row.Session {
				return claudeCompatibilityError(c, "managed upstream session identity conflict")
			}
			if gjson.GetBytes(body, "model").String() != p.History.Model {
				return claudeCompatibilityError(c, "managed conversation model mapping conflict")
			}

		} else if s.claudeRecovery != nil && session != "" && s.claudeRecovery.store != nil {
			managed, e := s.claudeRecovery.store.IsRecoveryUpstreamSession(ctx, session)
			if e != nil || managed {
				return claudeCompatibilityStatusError(c, 503, "managed upstream session cannot be used as a client conversation")
			}
		}
	}
	if err != nil {
		return claudeCompatibilityError(c, err.Error())
	}
	// Requests without a session identifier cannot assert cross-turn continuity.
	// The OAuth compatibility adapter generates and exposes one automatically.
	if session == "" {
		return nil
	}
	// A gateway-only resume header must also reach the upstream conversation.
	// Preserve native omission when the body already carries the same UUID.
	metadata := ParseMetadataUserID(gjson.GetBytes(body, "metadata.user_id").String())
	if len(headers) > 0 && (metadata == nil || metadata.SessionID == "") && getHeaderRaw(headers[0], "X-Claude-Code-Session-Id") == "" {
		setHeaderRaw(headers[0], "X-Claude-Code-Session-Id", session)
	}
	unavailable := func() error {
		return claudeCompatibilityStatusError(c, http.StatusServiceUnavailable, "Claude conversation state is unavailable; retry later")
	}
	if s.claudeSessionStore == nil {
		return unavailable()
	}
	if hasClaudeConversationHeader(c) {
		owner, err := s.claudeSessionStore.GetClaudeSessionAccountID(ctx, session)
		if err != nil {
			return unavailable()
		}
		if owner == 0 {
			return claudeCompatibilityError(c, "Claude conversation is unknown; start a new conversation")
		}
	}
	owner, err := s.claudeSessionStore.ClaimClaudeSessionAccountID(ctx, session, account.ID)
	if err != nil || owner <= 0 {
		return unavailable()
	}
	if owner != account.ID {
		return claudeCompatibilityStatusError(c, http.StatusServiceUnavailable, "Claude conversation's bound account is unavailable; retry later or start a new conversation")
	}
	return nil
}

// Reserve before returning a selected account, including mixed scheduling. This
// also covers handlers that perform work before building the messages request.
func (s *GatewayService) reserveClaudeSelectedAccount(ctx context.Context, sessionHash string, accountID int64) error {
	session := claudeSessionFromRoutingKey(sessionHash)
	if session == "" {
		return nil
	}
	if s.claudeSessionStore == nil {
		return fmt.Errorf("claude conversation store is unavailable")
	}
	owner, err := s.claudeSessionStore.ClaimClaudeSessionAccountID(ctx, session, accountID)
	if err != nil {
		return fmt.Errorf("reserve Claude conversation ownership: %w", err)
	}
	if owner <= 0 || owner != accountID {
		return fmt.Errorf("%w: Claude conversation cannot change accounts", ErrNoAvailableAccounts)
	}
	if p := ClaudeRecoveryFromContext(ctx); p != nil {
		a, e := s.accountRepo.GetByID(ctx, accountID)
		if e != nil {
			return e
		}
		if !recoveryAccountSupported(a) || !recoveryAccountInScope(a, p.Row.Scope) {
			return ErrRecoveryConflict
		}
		if e = p.manager.store.BindRecoveryAccount(ctx, p.Row, accountID, recoveryAccountPrincipal(a), p.ReadOnly); e != nil {
			return e
		}
	}
	return nil
}

func ResolveClaudeRecoveryClientID(ctx context.Context, c *gin.Context, body []byte) (string, error) {
	return claudeRequestSession(ctx, c, body)
}
