//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClaudeContinuityCanonicalSessionAcrossAdapters(t *testing.T) {
	const session = "abcdef12-3456-4789-abcd-0123456789ab"
	for _, route := range []string{"/v1/messages", "/v1/messages/count_tokens", "/v1/chat/completions", "/v1/responses"} {
		for _, spelling := range []string{strings.ToUpper(session), "urn:uuid:" + session, "{" + session + "}", strings.ReplaceAll(session, "-", "")} {
			t.Run(route+"/"+spelling, func(t *testing.T) {
				svc, up := newClaudeContractGateway(t)
				body := []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}],"input":"hello","metadata":{"user_id":%q}}`, fmt.Sprintf(`{"device_id":"test","account_uuid":"","session_id":%q}`, session)))
				_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), body, map[string]string{"X-Claude-Code-Session-Id": spelling}, route)
				require.NoError(t, err)
				require.Equal(t, 1, up.calls)
				require.Equal(t, session, rec.Header().Get(claudeConversationHeader))
				require.Equal(t, session, getHeaderRaw(up.request.Header, "X-Claude-Code-Session-Id"))
			})
		}
	}
}

func TestClaudeContinuityMetadataWhitespaceKeepsRoutingOwner(t *testing.T) {
	const session = "abcdef12-3456-4789-abcd-0123456789ab"
	for _, value := range []string{session, " \t" + session + "\n", "\u00a0" + session + "\u00a0"} {
		svc, _ := newClaudeContractGateway(t)
		body := []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-6","metadata":{"user_id":%q}}`, fmt.Sprintf(`{"device_id":"test","session_id":%q}`, value)))
		parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
		require.NoError(t, err)
		require.NoError(t, svc.ValidateClaudeSessionRouting(context.Background(), nil, body, parsed))
		require.Equal(t, session, parsed.ClaudeSessionID)
		require.Equal(t, session, svc.GenerateSessionHash(parsed), "metadata routing must agree with validation")
		_, err = svc.claudeSessionStore.ClaimClaudeSessionAccountID(context.Background(), session, 123)
		require.NoError(t, err)
		ctx, err := svc.withClaudeSessionOwner(context.Background(), svc.GenerateSessionHash(parsed))
		require.NoError(t, err)
		require.EqualValues(t, 123, claudeSessionOwner(ctx))
	}
}

func TestClaudeContinuityRejectsConflictsBeforePublishingSession(t *testing.T) {
	for name, values := range map[string][]string{
		"conflict": {"abcdef12-3456-4789-abcd-0123456789ab", "12345678-1234-4234-8234-123456789abc"},
		"invalid":  {"abcdef12-3456-4789-abcd-0123456789ab", "not-a-uuid"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
			c.Request.Header["X-Claude-Code-Session-Id"] = values
			_, err := prepareClaudeCompatibility(context.Background(), c, []byte(`{"model":"claude-sonnet-4-6"}`))
			require.Error(t, err)
			require.Equal(t, 400, rec.Code)
			require.Empty(t, rec.Header().Get(claudeConversationHeader))
			_, exists := c.Get(claudeCompatibilityGinKey)
			require.False(t, exists)
		})
	}
}

func TestClaudeContinuityRestoresFrozenProfileBeforeReadingSettings(t *testing.T) {
	claude.SetCLIVersionResolver(func() string { return "2.1.292" })
	t.Cleanup(func() { claude.SetCLIVersionResolver(nil) })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	body := []byte(`{"model":"claude-sonnet-4-6"}`)
	ctx, err := prepareClaudeCompatibility(context.Background(), c, body)
	require.NoError(t, err)
	want := claudeCompatibilityFromContext(ctx)
	reads := 0
	claude.SetCLIVersionResolver(func() string { reads++; return "2.1.300" })
	restored, err := prepareClaudeCompatibility(context.Background(), c, body)
	require.NoError(t, err)
	require.Same(t, want, claudeCompatibilityFromContext(restored))
	require.Zero(t, reads, "an already prepared request must not consult mutable settings again")
	// A different request must still reject an unverified explicit setting.
	next, _ := gin.CreateTestContext(httptest.NewRecorder())
	next.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	_, err = prepareClaudeCompatibility(context.Background(), next, body)
	require.ErrorContains(t, err, "unsupported Claude compatibility version")
}

func TestClaudeContinuityRepeatedResumeHeaderStillRequiresKnownOwner(t *testing.T) {
	const session = "abcdef12-3456-4789-abcd-0123456789ab"
	for _, header := range []string{claudeConversationHeader, strings.ToLower(claudeConversationHeader)} {
		t.Run(header, func(t *testing.T) {
			svc, up := newClaudeContractGateway(t)
			account := newClaude2292Account(AccountTypeOAuth)
			makeContext := func() *gin.Context {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
				c.Request.Header[header] = []string{" ", session}
				return c
			}
			require.ErrorContains(t, svc.ValidateClaudeSessionRouting(context.Background(), makeContext(), nil), "unknown")
			require.ErrorContains(t, svc.bindClaudeConversation(context.Background(), makeContext(), account, nil), "unknown")
			owner, err := svc.claudeSessionStore.GetClaudeSessionAccountID(context.Background(), session)
			require.NoError(t, err)
			require.Zero(t, owner)
			require.Zero(t, up.calls)
			_, err = svc.claudeSessionStore.ClaimClaudeSessionAccountID(context.Background(), session, account.ID)
			require.NoError(t, err)
			parsed := &ParsedRequest{SessionContext: &SessionContext{APIKeyID: 7}}
			require.NoError(t, svc.ValidateClaudeSessionRouting(context.Background(), makeContext(), nil, parsed))
			require.Equal(t, session, parsed.ClaudeSessionID)
			require.Equal(t, "claude-conversation:7:"+session, svc.GenerateSessionHash(parsed))
			require.NoError(t, svc.bindClaudeConversation(context.Background(), makeContext(), account, nil))
		})
	}
}

func exposedClaudeHeaders(h http.Header) []string {
	var names []string
	for _, value := range h.Values("Access-Control-Expose-Headers") {
		for _, name := range strings.Split(value, ",") {
			names = append(names, strings.ToLower(strings.TrimSpace(name)))
		}
	}
	return names
}

func TestClaudeContinuityExposesSessionWithExactHeaderTokens(t *testing.T) {
	for _, existing := range [][]string{
		{"X-Sub2API-Session-Id-Debug"},
		{"ETag", "Server-Timing"},
		{"ETag", "x-sub2api-session-id"},
	} {
		t.Run(strings.Join(existing, "/"), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
			c.Writer.Header()["Access-Control-Expose-Headers"] = append([]string(nil), existing...)
			_, err := prepareClaudeCompatibility(context.Background(), c, []byte(`{"model":"claude-sonnet-4-6"}`))
			require.NoError(t, err)
			names := exposedClaudeHeaders(rec.Header())
			require.Contains(t, names, strings.ToLower(claudeConversationHeader))
			for _, name := range existing {
				require.Contains(t, names, strings.ToLower(name))
			}
			count := 0
			for _, name := range names {
				if name == strings.ToLower(claudeConversationHeader) {
					count++
				}
			}
			require.Equal(t, 1, count)
		})
	}
}

func TestClaudeContinuityExposesReturnedRetrySignals(t *testing.T) {
	svc, _ := newClaudeContractGateway(t)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Writer.Header().Add("Access-Control-Expose-Headers", "ETag")
	headers := http.Header{
		"Request-Id": {"local-request"}, "Retry-After": {"2"},
		"Retry-After-Ms": {"1500"}, "X-Should-Retry": {"false"}, "Cf-Ray": {""},
	}
	resp := &http.Response{StatusCode: 429, Header: headers, Body: io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"rate_limit_error","message":"Local refusal"}}`))}
	require.Error(t, svc.returnClaudeUpstreamError(context.Background(), c, newClaude2292Account(AccountTypeOAuth), resp, "claude-sonnet-4-6"))
	require.Equal(t, 429, rec.Code)
	names := exposedClaudeHeaders(rec.Header())
	for key, values := range headers {
		require.Equal(t, values, rec.Header().Values(key))
		require.Contains(t, names, strings.ToLower(key))
	}
	require.Contains(t, names, "etag")
}

type claudeContinuityTransportFailure struct{ calls int }

func (u *claudeContinuityTransportFailure) Do(*http.Request, string, int64, int) (*http.Response, error) {
	u.calls++
	return nil, io.ErrUnexpectedEOF
}

func (u *claudeContinuityTransportFailure) DoWithTLS(r *http.Request, p string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(r, p, id, n)
}

func TestClaudeContinuityTransportFailureDoesNotRepeatOperation(t *testing.T) {
	for _, route := range []string{"/v1/messages", "/v1/messages/count_tokens", "/v1/chat/completions", "/v1/responses"} {
		t.Run(route, func(t *testing.T) {
			svc, _ := newClaudeContractGateway(t)
			up := &claudeContinuityTransportFailure{}
			svc.httpUpstream = up
			body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}],"input":"hello"}`)
			_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), body, nil, route)
			require.ErrorIs(t, err, io.ErrUnexpectedEOF)
			require.Equal(t, 1, up.calls)
			require.Equal(t, http.StatusBadGateway, rec.Code)
			require.NotEmpty(t, rec.Header().Get(claudeConversationHeader))
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover))
		})
	}
}
