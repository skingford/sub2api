//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type claudeContractUpstream struct {
	calls   int
	request *http.Request
	body    []byte
	status  int
}

func (u *claudeContractUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls++
	u.request = req
	var err error
	u.body, err = io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	status := u.status
	if status == 0 {
		status = 200
	}
	contentType := "application/json"
	payload := `{"id":"msg_contract","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"OK"}],"usage":{"input_tokens":1,"output_tokens":1},"stop_reason":"end_turn"}`
	if strings.Contains(req.URL.Path, "count_tokens") {
		payload = `{"input_tokens":128}`
	}
	if status >= 400 {
		payload = `{"type":"error","error":{"type":"permission_error","message":"Local synthetic refusal"}}`
	} else if gjson.GetBytes(u.body, "stream").Bool() {
		contentType = "text/event-stream"
		payload = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_contract\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-6\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}, "Retry-After": {"1"}}, Body: io.NopCloser(strings.NewReader(payload))}, nil
}
func (u *claudeContractUpstream) DoWithTLS(r *http.Request, p string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(r, p, id, n)
}

func newClaudeContractGateway(t *testing.T) (*GatewayService, *claudeContractUpstream) {
	t.Helper()
	resetGatewayForwardingSettingsCacheForTest(t)
	svc := newClaude2292Gateway()
	up := &claudeContractUpstream{}
	svc.httpUpstream = up
	svc.rateLimitService = nil
	svc.settingService = NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{}}, svc.cfg)
	svc.identityService = NewIdentityService(&claude2292IdentityCache{stubIdentityCache: stubIdentityCache{fingerprint: &Fingerprint{ClientID: strings.Repeat("d", 64), UserAgent: claude.DefaultUserAgent(), UpdatedAt: time.Now().Unix()}}})
	return svc, up
}
func callClaudeContract(t *testing.T, svc *GatewayService, account *Account, body []byte, headers map[string]string, route string) (*gin.Context, *httptest.ResponseRecorder, error) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", route, bytes.NewReader(body))
	for k, v := range headers {
		c.Request.Header.Set(k, v)
	}
	if strings.Contains(route, "chat/completions") {
		_, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, nil)
		return c, rec, err
	}
	if strings.Contains(route, "responses") {
		_, err := svc.ForwardAsResponses(context.Background(), c, account, body, nil)
		return c, rec, err
	}
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)
	if strings.Contains(route, "count_tokens") {
		return c, rec, svc.ForwardCountTokens(context.Background(), c, account, parsed)
	}
	_, err = svc.Forward(context.Background(), c, account, parsed)
	return c, rec, err
}

func TestClaude2292ContractModelProfiles(t *testing.T) {
	for _, model := range []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5-20251001"} {
		t.Run(model, func(t *testing.T) {
			svc, up := newClaudeContractGateway(t)
			input := []byte(`{"model":"` + model + `","messages":[{"role":"user","content":"hello"}]}`)
			c, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), input, nil, "/v1/messages")
			require.NoError(t, err)
			require.Equal(t, 1, up.calls)
			require.Equal(t, "0.128.0", getHeaderRaw(up.request.Header, "X-Stainless-Package-Version"))
			require.Equal(t, "v26.3.0", getHeaderRaw(up.request.Header, "X-Stainless-Runtime-Version"))
			require.Equal(t, "x64", getHeaderRaw(up.request.Header, "X-Stainless-Arch"))
			require.Equal(t, HTTPUpstreamProfileClaude2292, HTTPUpstreamProfileFromContext(up.request.Context()))
			require.False(t, preserveNativeClaudeRequest(up.request.Context(), c, newClaude2292Account(AccountTypeOAuth), up.body), "generated billing must not reclassify the input")
			require.Contains(t, gjson.GetBytes(up.body, "system.0.text").String(), " cch=")
			metadata := ParseMetadataUserID(gjson.GetBytes(up.body, "metadata.user_id").String())
			require.NotNil(t, metadata)
			require.Equal(t, rec.Header().Get(claudeConversationHeader), metadata.SessionID)
			require.Equal(t, metadata.SessionID, getHeaderRaw(up.request.Header, "X-Claude-Code-Session-Id"))
			require.Equal(t, "main", getHeaderRaw(up.request.Header, "x-claude-code-request-class"))
			_, err = uuid.Parse(getHeaderRaw(up.request.Header, "x-claude-code-prompt-id"))
			require.NoError(t, err)
			raw, err := os.ReadFile("testdata/claude_code_2_1_292/model-defaults.json")
			require.NoError(t, err)
			var fixtures map[string]struct {
				Body json.RawMessage
				Beta string
			}
			require.NoError(t, json.Unmarshal(raw, &fixtures))
			native := fixtures[model]
			for _, field := range []string{"max_tokens", "thinking", "output_config", "context_management"} {
				want, got := gjson.GetBytes(native.Body, field), gjson.GetBytes(up.body, field)
				require.Equal(t, want.Exists(), got.Exists(), field)
				if want.Exists() {
					require.JSONEq(t, want.Raw, got.Raw, field)
				}
			}
			require.False(t, gjson.GetBytes(up.body, "temperature").Exists())
			expectedBeta := strings.ReplaceAll(native.Beta, ","+claude.BetaCacheDiagnosis, "")
			require.Equal(t, expectedBeta, getHeaderRaw(up.request.Header, "anthropic-beta"), "optional diagnostic state is not fabricated")
			if folder := os.Getenv("CLAUDE_CONTRACT_CAPTURE_DIR"); folder != "" {
				headers := map[string]string{}
				for key, values := range up.request.Header {
					if !strings.EqualFold(key, "authorization") && !strings.EqualFold(key, "x-api-key") {
						headers[key] = strings.Join(values, ", ")
					}
				}
				record := map[string]any{"method": up.request.Method, "path": up.request.URL.RequestURI(), "headers": headers, "raw_body_utf8": string(up.body), "upstream_profile": HTTPUpstreamProfileFromContext(up.request.Context()), "synthetic_auth": "oauth"}
				raw, err := json.MarshalIndent(record, "", "  ")
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(folder, model+".json"), append(raw, '\n'), 0600))
			}
		})
	}
}

func TestClaude2292ContractSessionLifecycle(t *testing.T) {
	svc, up := newClaudeContractGateway(t)
	account := newClaude2292Account(AccountTypeOAuth)
	input := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}]}`)
	_, first, err := callClaudeContract(t, svc, account, input, nil, "/v1/messages")
	require.NoError(t, err)
	firstID := first.Header().Get(claudeConversationHeader)
	require.NotEmpty(t, firstID)
	_, second, err := callClaudeContract(t, svc, account, input, nil, "/v1/messages")
	require.NoError(t, err)
	require.NotEqual(t, firstID, second.Header().Get(claudeConversationHeader), "same opening text must not merge independent conversations")
	continued, err := sjson.SetRawBytes(input, "messages.1", []byte(`{"role":"assistant","content":"OK"}`))
	require.NoError(t, err)
	_, third, err := callClaudeContract(t, svc, account, continued, map[string]string{claudeConversationHeader: firstID}, "/v1/messages")
	require.NoError(t, err)
	require.Equal(t, firstID, third.Header().Get(claudeConversationHeader))
	require.Equal(t, firstID, ParseMetadataUserID(gjson.GetBytes(up.body, "metadata.user_id").String()).SessionID)
	before := up.calls
	_, invalid, err := callClaudeContract(t, svc, account, input, map[string]string{claudeConversationHeader: "invalid"}, "/v1/messages")
	require.Error(t, err)
	require.Equal(t, 400, invalid.Code)
	require.Equal(t, before, up.calls)
}

func TestClaude2292ContractRejectsAccountConflict(t *testing.T) {
	svc, up := newClaudeContractGateway(t)
	capture := loadNativeClaudeCapture(t, "2_1_292", "validation-oauth-synthetic-01")
	uid := gjson.GetBytes(capture.Body, "metadata.user_id").String()
	uid, err := sjson.Set(uid, "account_uuid", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	require.NoError(t, err)
	body, err := sjson.SetBytes(capture.Body, "metadata.user_id", uid)
	require.NoError(t, err)
	_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), body, capture.Headers, "/v1/messages")
	require.Error(t, err)
	require.Equal(t, 400, rec.Code)
	require.Zero(t, up.calls)
	require.Contains(t, rec.Body.String(), "does not match")
}

func TestClaude2292ContractCallerOwnsErrorRetries(t *testing.T) {
	for _, status := range []int{403, 503, 529} {
		for _, route := range []string{"/v1/messages", "/v1/messages/count_tokens", "/v1/chat/completions", "/v1/responses"} {
			t.Run(route+http.StatusText(status), func(t *testing.T) {
				svc, up := newClaudeContractGateway(t)
				up.status = status
				body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}]}`)
				if strings.Contains(route, "responses") {
					body = []byte(`{"model":"claude-sonnet-4-6","input":"hello"}`)
				}
				_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), body, nil, route)
				require.Error(t, err)
				require.Equal(t, 1, up.calls)
				require.Equal(t, status, rec.Code)
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover))
			})
		}
	}
}

func TestClaude2292ContractExplicitControls(t *testing.T) {
	for _, model := range []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5-20251001"} {
		body := []byte(`{"model":"` + model + `","max_tokens":2048,"temperature":0.4,"thinking":{"type":"disabled"},"output_config":{"effort":"low"},"messages":[{"role":"user","content":"hello"}]}`)
		out, _ := normalizeClaudeOAuthRequestBody(body, model, claudeOAuthNormalizeOptions{})
		for _, field := range []string{"max_tokens", "temperature", "thinking", "output_config"} {
			require.Equal(t, gjson.GetBytes(body, field).Raw, gjson.GetBytes(out, field).Raw)
		}
	}
}

func TestClaude2292ContractUnknownProfileRejected(t *testing.T) {
	claude.SetCLIVersionResolver(func() string { return "9.9.9" })
	t.Cleanup(func() { claude.SetCLIVersionResolver(nil) })
	svc, up := newClaudeContractGateway(t)
	_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}]}`), nil, "/v1/messages")
	require.Error(t, err)
	require.Equal(t, 400, rec.Code)
	require.Zero(t, up.calls)
}

func TestClaude2292ContractNativeCountCompleteHeaderSet(t *testing.T) {
	for _, name := range []string{"oauth-count-native-01", "oauth-count-native-02", "oauth-count-native-03"} {
		capture := loadNativeClaudeCapture(t, "2_1_292", name)
		req, body := forwardClaude2292Capture(t, newClaude2292Gateway(), newClaude2292Account(AccountTypeOAuth), capture)
		require.Equal(t, []byte(capture.Body), body)
		want := map[string][]string{}
		for k, v := range capture.Headers {
			want[strings.ToLower(k)] = []string{v}
		}
		got := map[string][]string{}
		for k, v := range req.Header {
			if strings.EqualFold(k, "authorization") {
				continue
			}
			got[strings.ToLower(k)] = v
		}
		require.Equal(t, want, got, "both missing and additional headers matter")
	}
}

func TestClaude2292ContractProfileIsFrozenDuringRequest(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}]}`)
	ctx, err := prepareClaudeCompatibility(context.Background(), c, body)
	require.NoError(t, err)
	claude.SetCLIVersionResolver(func() string { return "9.9.9" })
	t.Cleanup(func() { claude.SetCLIVersionResolver(nil) })
	svc, _ := newClaudeContractGateway(t)
	req, _, err := svc.buildUpstreamRequest(ctx, c, newClaude2292Account(AccountTypeOAuth), body, "synthetic-token", "oauth", "claude-sonnet-4-6", false, true)
	require.NoError(t, err)
	require.Contains(t, getHeaderRaw(req.Header, "User-Agent"), "2.1.292")
	require.Equal(t, "0.128.0", getHeaderRaw(req.Header, "X-Stainless-Package-Version"))
	require.Equal(t, HTTPUpstreamProfileClaude2292, HTTPUpstreamProfileFromContext(req.Context()))
}

func TestClaude2292ContractOpaqueUserLabelIsNotNativeIdentity(t *testing.T) {
	svc, up := newClaudeContractGateway(t)
	input := []byte(`{"model":"claude-sonnet-4-6","metadata":{"user_id":"application-user-label"},"messages":[{"role":"user","content":"hello"}]}`)
	_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), input, nil, "/v1/messages")
	require.NoError(t, err)
	uid := ParseMetadataUserID(gjson.GetBytes(up.body, "metadata.user_id").String())
	require.NotNil(t, uid)
	require.Equal(t, rec.Header().Get(claudeConversationHeader), uid.SessionID)
	require.Equal(t, "11111111-1111-4111-8111-111111111111", uid.AccountUUID)
}

type claudeConversationCacheForTest struct {
	GatewayCache
	bindings map[string]int64
}

func (s *claudeConversationCacheForTest) GetSessionAccountID(_ context.Context, group int64, key string) (int64, error) {
	id, ok := s.bindings[fmt.Sprintf("%d:%s", group, key)]
	if !ok {
		return 0, ErrStickySessionNotFound
	}
	return id, nil
}
func (s *claudeConversationCacheForTest) SetSessionAccountID(_ context.Context, group int64, key string, id int64, _ time.Duration) error {
	s.bindings[fmt.Sprintf("%d:%s", group, key)] = id
	return nil
}

func TestClaude2292ContractSessionBindingCannotRotateAccounts(t *testing.T) {
	svc, up := newClaudeContractGateway(t)
	cache := &claudeConversationCacheForTest{bindings: map[string]int64{}}
	svc.cache = cache
	input := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}]}`)
	account := newClaude2292Account(AccountTypeOAuth)
	_, first, err := callClaudeContract(t, svc, account, input, nil, "/v1/messages")
	require.NoError(t, err)
	sid := first.Header().Get(claudeConversationHeader)
	key := claudeConversationRoutingKey(0, sid)
	bound, err := svc.claudeSessionStore.GetClaudeSessionAccountID(context.Background(), sid)
	require.NoError(t, err)
	require.Equal(t, account.ID, bound)
	_, _, err = callClaudeContract(t, svc, account, input, map[string]string{claudeConversationHeader: sid}, "/v1/messages")
	require.NoError(t, err)
	other := newClaude2292Account(AccountTypeOAuth)
	other.ID = 999
	// Simulate mutable scheduler affinity being replaced before forwarding.
	require.NoError(t, cache.SetSessionAccountID(context.Background(), 0, key, other.ID, time.Hour))
	before := up.calls
	_, rec, err := callClaudeContract(t, svc, other, input, map[string]string{claudeConversationHeader: sid}, "/v1/messages")
	require.Error(t, err)
	require.Equal(t, 503, rec.Code)
	require.Equal(t, before, up.calls)
	// Expiring or clearing the scheduler cache cannot remove database ownership.
	cache.bindings = map[string]int64{}
	_, rec, err = callClaudeContract(t, svc, account, input, map[string]string{claudeConversationHeader: sid}, "/v1/messages")
	require.NoError(t, err)
	require.Equal(t, before+1, up.calls)

}

func TestClaude2292ContractSessionRoutingUsesCallerScope(t *testing.T) {
	svc := &GatewayService{claudeSessionStore: &memoryClaudeSessionStore{}}
	parsed := &ParsedRequest{ClaudeSessionID: "11111111-1111-4111-8111-111111111111", SessionContext: &SessionContext{APIKeyID: 7}}
	first := svc.GenerateSessionHash(parsed)
	require.Equal(t, claudeConversationRoutingKey(7, parsed.ClaudeSessionID), first)
	parsed.SessionContext.APIKeyID = 8
	require.NotEqual(t, first, svc.GenerateSessionHash(parsed))
	parsed.ClaudeSessionID = "11111111-1111-4111-8111-111111111111:identity"
	require.Empty(t, svc.GenerateSessionHash(parsed), "invalid values cannot address internal binding keys")
}

func TestClaude2292ContractCallerSuppliedConversationAndPrompt(t *testing.T) {
	svc, up := newClaudeContractGateway(t)
	sid, prompt := uuid.NewString(), uuid.NewString()
	headers := map[string]string{"X-Claude-Code-Session-Id": sid, "x-claude-code-prompt-id": prompt}
	for range 2 {
		_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}]}`), headers, "/v1/messages")
		require.NoError(t, err)
		require.Equal(t, sid, rec.Header().Get(claudeConversationHeader))
		require.Equal(t, sid, ParseMetadataUserID(gjson.GetBytes(up.body, "metadata.user_id").String()).SessionID)
		require.Equal(t, prompt, getHeaderRaw(up.request.Header, "x-claude-code-prompt-id"))
	}
}

func TestClaude2292ContractNativeCountUsesMessageAffinity(t *testing.T) {
	sid := uuid.NewString()
	svc := &GatewayService{claudeSessionStore: &memoryClaudeSessionStore{}}
	messages := &ParsedRequest{MetadataUserID: FormatMetadataUserID(strings.Repeat("d", 64), "", sid, "2.1.292")}
	count := &ParsedRequest{ClaudeSessionID: sid, SessionContext: &SessionContext{APIKeyID: 7, NativeClaude: true}}
	require.Equal(t, svc.GenerateSessionHash(messages), svc.GenerateSessionHash(count))
}

func TestClaude2292ContractMetadataCannotAddressBindingNamespace(t *testing.T) {
	svc := &GatewayService{claudeSessionStore: &memoryClaudeSessionStore{}}
	parsed := &ParsedRequest{MetadataUserID: `{"device_id":"local","account_uuid":"","session_id":"claude-conversation:7:11111111-1111-4111-8111-111111111111:identity"}`}
	require.Empty(t, svc.GenerateSessionHash(parsed))
}
