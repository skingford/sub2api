package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAnthropicClientRequestIDOrigin(t *testing.T) {
	for _, tc := range []struct {
		url  string
		want bool
	}{
		{"https://api.anthropic.com/v1/messages", true},
		{"https://API.ANTHROPIC.COM:443/v1/messages/count_tokens", true},
		{"http://api.anthropic.com/v1/messages", false},
		{"https://api.anthropic.com:8443/v1/messages", false},
		{"https://api.anthropic.com.proxy.example/v1/messages", false},
		{"https://api.anthropic.com@proxy.example/v1/messages", false},
		{"https://user@api.anthropic.com/v1/messages", false},
		{"https://proxy.example/v1/messages", false},
		{"http://127.0.0.1:8080/v1/messages", false},
		{"http://[::1]:8080/v1/messages", false},
	} {
		t.Run(tc.url, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.url, nil)
			ensureAnthropicClientRequestID(req)
			id := getHeaderRaw(req.Header, "x-client-request-id")
			if !tc.want {
				require.Empty(t, id)
				return
			}
			parsed, err := uuid.Parse(id)
			require.NoError(t, err)
			require.Equal(t, uuid.Version(4), parsed.Version())
			ensureAnthropicClientRequestID(req)
			require.Equal(t, id, getHeaderRaw(req.Header, "x-client-request-id"))
			other := httptest.NewRequest(http.MethodPost, tc.url, nil)
			ensureAnthropicClientRequestID(other)
			require.NotEqual(t, id, getHeaderRaw(other.Header, "x-client-request-id"))
		})
	}
}

func TestAnthropicClientRequestIDPreservesExistingHeader(t *testing.T) {
	for _, key := range []string{"x-client-request-id", "X-Client-Request-Id", "X-CLIENT-REQUEST-ID"} {
		for _, value := range []string{"caller-correlation-id", ""} {
			req := httptest.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", nil)
			req.Header[key] = []string{value}
			before := req.Header.Clone()
			ensureAnthropicClientRequestID(req)
			require.Equal(t, before, req.Header, "an explicitly present header must survive, including an empty value")
		}
	}
}

func TestAnthropicClientRequestIDBuilders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, route := range []string{"messages", "messages_passthrough", "count_tokens", "count_tokens_passthrough"} {
		for _, tc := range []struct {
			name     string
			baseURL  string
			incoming string
			override string
			want     string
			generate bool
		}{
			{name: "canonical", baseURL: "https://api.anthropic.com", generate: true},
			{name: "relay", baseURL: "https://relay.example"},
			{name: "caller_id", baseURL: "https://api.anthropic.com", incoming: "caller-id", want: "caller-id"},
			{name: "relay_caller_id", baseURL: "https://relay.example", incoming: "caller-id", want: "caller-id"},
			{name: "blocked_account_override", baseURL: "https://api.anthropic.com", incoming: "caller-id", override: "configured-id", want: "caller-id"},
			{name: "blocked_override_without_caller", baseURL: "https://api.anthropic.com", override: "configured-id", generate: true},
		} {
			t.Run(route+"/"+tc.name, func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				if tc.incoming != "" {
					c.Request.Header.Set("X-Client-Request-Id", tc.incoming)
				}
				account := newAnthropicAPIKeyAccountForTest()
				account.Credentials["base_url"] = tc.baseURL
				if tc.override != "" {
					account.Credentials[credKeyHeaderOverrideEnabled] = true
					account.Credentials[credKeyHeaderOverrides] = map[string]any{"x-client-request-id": tc.override}
				}
				svc := &GatewayService{cfg: &config.Config{}}
				body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}],"max_tokens":32}`)
				var req *http.Request
				var err error
				switch route {
				case "messages":
					req, _, err = svc.buildUpstreamRequest(context.Background(), c, account, body, "synthetic-key", "apikey", "claude-sonnet-4-6", false, false)
				case "messages_passthrough":
					req, _, err = svc.buildUpstreamRequestAnthropicAPIKeyPassthrough(context.Background(), c, account, body, "synthetic-key")
				case "count_tokens":
					req, _, err = svc.buildCountTokensRequest(context.Background(), c, account, body, "synthetic-key", "apikey", "claude-sonnet-4-6", false)
				case "count_tokens_passthrough":
					req, err = svc.buildCountTokensRequestAnthropicAPIKeyPassthrough(context.Background(), c, account, body, "synthetic-key")
				}
				require.NoError(t, err)
				id := getHeaderRaw(req.Header, "x-client-request-id")
				if tc.generate {
					_, err := uuid.Parse(id)
					require.NoError(t, err)
				} else {
					require.Equal(t, tc.want, id)
				}
				require.Empty(t, getHeaderRaw(req.Header, "x-stainless-helper-method"))
			})
		}
	}
}
