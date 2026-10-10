//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type claudeErrorParityUpstream struct {
	claudeContractUpstream
	headers      http.Header
	responseBody func() io.ReadCloser
	closes       int
}

type claudeErrorParityBody struct {
	io.Reader
	err    error
	closed *int
}

func (r *claudeErrorParityBody) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF && r.err != nil {
		return n, r.err
	}
	return n, err
}
func (r *claudeErrorParityBody) Close() error { *r.closed++; return nil }

func (u *claudeErrorParityUpstream) Do(req *http.Request, proxy string, account int64, concurrency int) (*http.Response, error) {
	resp, err := u.claudeContractUpstream.Do(req, proxy, account, concurrency)
	if err != nil {
		return resp, err
	}
	_ = resp.Body.Close()
	resp.Body = u.responseBody()
	resp.Header = u.headers.Clone()
	return resp, nil
}
func (u *claudeErrorParityUpstream) DoWithTLS(req *http.Request, proxy string, account int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, account, concurrency)
}

type claudeErrorParityAccountRepo struct {
	AccountRepository
	rateLimitCalls int
	accountID      int64
}

func (r *claudeErrorParityAccountRepo) SetRateLimited(_ context.Context, id int64, _ time.Time) error {
	r.rateLimitCalls++
	r.accountID = id
	return nil
}

func TestClaudeErrorParityAcrossEntrypoints(t *testing.T) {
	for _, route := range []string{"/v1/messages", "/v1/messages/count_tokens", "/v1/chat/completions", "/v1/responses"} {
		for _, mode := range []string{"complete", "interrupted", "oversized"} {
			t.Run(route+"/"+mode, func(t *testing.T) {
				svc, _ := newClaudeContractGateway(t)
				repo := &claudeErrorParityAccountRepo{}
				svc.rateLimitService = NewRateLimitService(repo, nil, svc.cfg, nil, nil)
				up := &claudeErrorParityUpstream{headers: http.Header{
					"Request-Id": {"req_local"}, "X-Request-Id": {"alternate_local"},
					"Retry-After": {"3"}, "Retry-After-Ms": {"2500"}, "X-Should-Retry": {"false"}, "Cf-Ray": {""},
					"Set-Cookie": {"must-not-forward"},
				}}
				up.status = http.StatusTooManyRequests
				payload := `{"type":"error","error":{"type":"rate_limit_error","message":"Local refusal https://example.invalid/?key=private"}}`
				var readErr error
				if mode == "interrupted" {
					readErr = io.ErrUnexpectedEOF
				}
				if mode == "oversized" {
					payload += strings.Repeat(" ", int(gatewayUpstreamErrorBodyReadLimit))
				}
				up.responseBody = func() io.ReadCloser {
					return &claudeErrorParityBody{Reader: strings.NewReader(payload), err: readErr, closed: &up.closes}
				}
				svc.httpUpstream = up
				account := newClaude2292Account(AccountTypeOAuth)
				body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}],"input":"hello","stream":true}`)
				c, rec, err := callClaudeContract(t, svc, account, body, nil, route)
				require.Error(t, err)
				require.Equal(t, 429, rec.Code)
				require.Equal(t, 1, up.calls)
				require.Equal(t, 1, up.closes)
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover))
				require.True(t, IsResponseCommitted(c))
				for _, key := range []string{"Request-Id", "X-Request-Id", "Retry-After", "Retry-After-Ms", "X-Should-Retry", "Cf-Ray"} {
					require.Equal(t, up.headers.Values(key), rec.Header().Values(key), key)
					require.Contains(t, exposedClaudeHeaders(rec.Header()), strings.ToLower(key), key)
				}
				require.Empty(t, rec.Header().Get("Set-Cookie"))
				require.Equal(t, 1, repo.rateLimitCalls)
				require.Equal(t, account.ID, repo.accountID)
				require.NotContains(t, rec.Body.String(), "private")
				require.NotContains(t, rec.Body.String(), "event:")
				if strings.Contains(route, "chat/completions") {
					require.Equal(t, "server_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
				} else if strings.Contains(route, "responses") {
					require.Equal(t, "server_error", gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
				} else {
					require.Equal(t, "error", gjson.GetBytes(rec.Body.Bytes(), "type").String())
				}
				if mode != "complete" {
					require.Contains(t, gjson.GetBytes(rec.Body.Bytes(), "error.message").String(), "incomplete")
					require.NotEmpty(t, c.GetString(OpsUpstreamErrorDetailKey))
				}
			})
		}
	}
}

func TestClaudeErrorParityIncompleteBodyCannotTriggerRecovery(t *testing.T) {
	for _, route := range []string{"/v1/messages", "/v1/messages/count_tokens", "/v1/chat/completions", "/v1/responses"} {
		for _, interrupted := range []bool{false, true} {
			name := route + "/complete"
			if interrupted {
				name = route + "/interrupted"
			}
			t.Run(name, func(t *testing.T) {
				svc, _ := newClaudeContractGateway(t)
				up := &claudeErrorParityUpstream{headers: http.Header{"Retry-After": {"1"}}}
				up.status = 400
				var readErr error
				if interrupted {
					readErr = io.ErrUnexpectedEOF
				}
				up.responseBody = func() io.ReadCloser {
					return &claudeErrorParityBody{Reader: strings.NewReader(`{"type":"error","error":{"type":"invalid_request_error","message":"The request body is not valid JSON"}}`), err: readErr, closed: &up.closes}
				}
				svc.httpUpstream = up
				_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}],"input":"hello"}`), nil, route)
				require.Error(t, err)
				require.Equal(t, 400, rec.Code)
				require.Equal(t, 1, up.calls)
				require.Equal(t, 1, up.closes)
				message := gjson.GetBytes(rec.Body.Bytes(), "error.message").String()
				if interrupted {
					require.Contains(t, message, "incomplete")
					require.False(t, claudeInvalidJSONRejection(rec.Body.Bytes()))
				} else {
					require.Equal(t, claudeInvalidJSONMessage, message)
				}
			})
		}
	}
}

func TestClaudeErrorParityNativeAPIKeyReadFailure(t *testing.T) {
	for route, fixture := range map[string]string{"/v1/messages": "validation-baseline-01", "/v1/messages/count_tokens": "validation-count-context-01"} {
		t.Run(route, func(t *testing.T) {
			capture := loadNativeClaudeCapture(t, "2_1_292", fixture)
			svc, _ := newClaudeContractGateway(t)
			up := &claudeErrorParityUpstream{headers: http.Header{"Retry-After": {"5"}, "X-Should-Retry": {"false"}}}
			up.status = 503
			up.responseBody = func() io.ReadCloser {
				return &claudeErrorParityBody{Reader: strings.NewReader("partial response"), err: io.ErrUnexpectedEOF, closed: &up.closes}
			}
			svc.httpUpstream = up
			_, rec, err := callNativeClaudeErrorContract(t, svc, capture, route)
			require.Error(t, err)
			require.Equal(t, 503, rec.Code)
			require.Equal(t, 1, up.calls)
			require.Equal(t, 1, up.closes)
			require.Equal(t, "5", rec.Header().Get("Retry-After"))
			require.Equal(t, "false", rec.Header().Get("X-Should-Retry"))
			require.Contains(t, rec.Body.String(), "incomplete")
		})
	}
}

func TestClaudeErrorBodyReadLimitBounds(t *testing.T) {
	svc := &GatewayService{}
	for _, size := range []int64{gatewayUpstreamErrorBodyReadLimit - 1, gatewayUpstreamErrorBodyReadLimit, gatewayUpstreamErrorBodyReadLimit + 2} {
		reader := strings.NewReader(strings.Repeat("x", int(size)))
		resp := &http.Response{Body: io.NopCloser(reader)}
		body, err := svc.readClaudeUpstreamErrorBody(resp)
		require.Len(t, body, int(min(size, gatewayUpstreamErrorBodyReadLimit)))
		if size > gatewayUpstreamErrorBodyReadLimit {
			require.ErrorIs(t, err, errClaudeErrorBodyTooLarge)
			require.EqualValues(t, 1, reader.Len())
		} else {
			require.NoError(t, err)
		}
	}
}

func TestClaudeErrorParityPreservesNonNativeAPIKeyFailover(t *testing.T) {
	for _, route := range []string{"/v1/chat/completions", "/v1/responses"} {
		svc, up := newClaudeContractGateway(t)
		up.status = 503
		_, _, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeAPIKey), []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}],"input":"hello"}`), nil, route)
		var failover *UpstreamFailoverError
		require.ErrorAs(t, err, &failover, route)
		require.Equal(t, 1, up.calls)
	}
}

func TestClaudeErrorParityNativeCountUnsupportedFallback(t *testing.T) {
	capture := loadNativeClaudeCapture(t, "2_1_292", "validation-count-context-01")
	svc, _ := newClaudeContractGateway(t)
	up := &claudeErrorParityUpstream{headers: http.Header{"Request-Id": {"req_count_unsupported"}}}
	up.status = 404
	up.responseBody = func() io.ReadCloser {
		return &claudeErrorParityBody{Reader: strings.NewReader(`{"error":{"type":"not_found_error","message":"/v1/messages/count_tokens not found"}}`), closed: &up.closes}
	}
	svc.httpUpstream = up
	c, rec, err := callNativeClaudeErrorContract(t, svc, capture, "/v1/messages/count_tokens")
	require.NoError(t, err)
	require.Equal(t, 404, rec.Code)
	require.Equal(t, 1, up.calls)
	require.Equal(t, 1, up.closes)
	require.True(t, IsResponseCommitted(c))
	require.Equal(t, "not_found_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Equal(t, "req_count_unsupported", rec.Header().Get("Request-Id"))
	_, exists := c.Get(OpsUpstreamStatusCodeKey)
	require.False(t, exists, "supported local fallback must not be logged as an upstream failure")
}

// Match the real handler's validated context. Native count probes omit metadata
// and must not be classified by the simpler body-only service test helper.
func callNativeClaudeErrorContract(t *testing.T, svc *GatewayService, capture claudeCapturedRequest, route string) (*gin.Context, *httptest.ResponseRecorder, error) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", route, bytes.NewReader(capture.Body))
	for key, value := range capture.Headers {
		c.Request.Header.Set(key, value)
	}
	var body map[string]any
	require.NoError(t, json.Unmarshal(capture.Body, &body))
	validated := NewClaudeCodeValidator().Validate(c.Request, body)
	require.True(t, validated, "the unmodified native capture must pass the production validator")
	ctx := SetClaudeCodeClient(c.Request.Context(), validated)
	ctx = SetClaudeCodeVersion(ctx, ExtractCLIVersion(c.GetHeader("User-Agent")))
	c.Request = c.Request.WithContext(ctx)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(capture.Body), PlatformAnthropic)
	require.NoError(t, err)
	account := newClaude2292Account(AccountTypeAPIKey)
	if strings.Contains(route, "count_tokens") {
		return c, rec, svc.ForwardCountTokens(ctx, c, account, parsed)
	}
	_, err = svc.Forward(ctx, c, account, parsed)
	return c, rec, err
}
