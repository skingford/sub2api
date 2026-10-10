package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func init() {
	// cors_test 与 security_headers_test 在同一个包，但 init 是幂等的
	gin.SetMode(gin.TestMode)
}

// --- Task 8.2: 验证 CORS 条件化头部 ---

func TestCORS_DisallowedOrigin_NoAllowHeaders(t *testing.T) {
	cfg := config.CORSConfig{
		AllowedOrigins:   []string{"https://allowed.example.com"},
		AllowCredentials: false,
	}
	middleware := CORS(cfg)

	tests := []struct {
		name   string
		method string
		origin string
	}{
		{
			name:   "preflight_disallowed_origin",
			method: http.MethodOptions,
			origin: "https://evil.example.com",
		},
		{
			name:   "get_disallowed_origin",
			method: http.MethodGet,
			origin: "https://evil.example.com",
		},
		{
			name:   "post_disallowed_origin",
			method: http.MethodPost,
			origin: "https://attacker.example.com",
		},
		{
			name:   "preflight_no_origin",
			method: http.MethodOptions,
			origin: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(tt.method, "/", nil)
			if tt.origin != "" {
				c.Request.Header.Set("Origin", tt.origin)
			}

			middleware(c)

			// 不应设置 Allow-Headers、Allow-Methods 和 Max-Age
			assert.Empty(t, w.Header().Get("Access-Control-Allow-Headers"),
				"不允许的 origin 不应收到 Allow-Headers")
			assert.Empty(t, w.Header().Get("Access-Control-Allow-Methods"),
				"不允许的 origin 不应收到 Allow-Methods")
			assert.Empty(t, w.Header().Get("Access-Control-Max-Age"),
				"不允许的 origin 不应收到 Max-Age")
			assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"),
				"不允许的 origin 不应收到 Allow-Origin")
		})
	}
}

func TestCORS_AllowedOrigin_HasAllowHeaders(t *testing.T) {
	cfg := config.CORSConfig{
		AllowedOrigins:   []string{"https://allowed.example.com"},
		AllowCredentials: false,
	}
	middleware := CORS(cfg)

	tests := []struct {
		name   string
		method string
	}{
		{name: "preflight_OPTIONS", method: http.MethodOptions},
		{name: "normal_GET", method: http.MethodGet},
		{name: "normal_POST", method: http.MethodPost},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(tt.method, "/", nil)
			c.Request.Header.Set("Origin", "https://allowed.example.com")

			middleware(c)

			// 应设置 Allow-Headers、Allow-Methods 和 Max-Age
			assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Headers"),
				"允许的 origin 应收到 Allow-Headers")
			assert.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "X-Admin-UI-Request")
			assert.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "X-User-UI-Request")
			assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Methods"),
				"允许的 origin 应收到 Allow-Methods")
			assert.Contains(t, w.Header().Get("Access-Control-Expose-Headers"), "Server-Timing")
			assert.Equal(t, "86400", w.Header().Get("Access-Control-Max-Age"),
				"允许的 origin 应收到 Max-Age=86400")
			assert.Equal(t, "https://allowed.example.com", w.Header().Get("Access-Control-Allow-Origin"),
				"允许的 origin 应收到 Allow-Origin")
		})
	}
}

func TestCORS_PreflightDisallowedOrigin_ReturnsForbidden(t *testing.T) {
	cfg := config.CORSConfig{
		AllowedOrigins:   []string{"https://allowed.example.com"},
		AllowCredentials: false,
	}
	middleware := CORS(cfg)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodOptions, "/", nil)
	c.Request.Header.Set("Origin", "https://evil.example.com")

	middleware(c)

	assert.Equal(t, http.StatusForbidden, w.Code,
		"不允许的 origin 的 preflight 请求应返回 403")
}

func TestCORS_PreflightAllowedOrigin_ReturnsNoContent(t *testing.T) {
	cfg := config.CORSConfig{
		AllowedOrigins:   []string{"https://allowed.example.com"},
		AllowCredentials: false,
	}
	middleware := CORS(cfg)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodOptions, "/", nil)
	c.Request.Header.Set("Origin", "https://allowed.example.com")

	middleware(c)

	assert.Equal(t, http.StatusNoContent, w.Code,
		"允许的 origin 的 preflight 请求应返回 204")
}

func TestCORS_WildcardOrigin_AllowsAny(t *testing.T) {
	cfg := config.CORSConfig{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: false,
	}
	middleware := CORS(cfg)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("Origin", "https://any-origin.example.com")

	middleware(c)

	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"),
		"通配符配置应返回 *")
	assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Headers"),
		"通配符 origin 应设置 Allow-Headers")
	assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Methods"),
		"通配符 origin 应设置 Allow-Methods")
}

func TestCORS_AllowCredentials_SetCorrectly(t *testing.T) {
	cfg := config.CORSConfig{
		AllowedOrigins:   []string{"https://allowed.example.com"},
		AllowCredentials: true,
	}
	middleware := CORS(cfg)

	t.Run("allowed_origin_gets_credentials", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Request.Header.Set("Origin", "https://allowed.example.com")

		middleware(c)

		assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"),
			"允许的 origin 且开启 credentials 应设置 Allow-Credentials")
	})

	t.Run("disallowed_origin_no_credentials", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Request.Header.Set("Origin", "https://evil.example.com")

		middleware(c)

		assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"),
			"不允许的 origin 不应收到 Allow-Credentials")
	})
}

func TestCORS_WildcardWithCredentials_DisablesCredentials(t *testing.T) {
	cfg := config.CORSConfig{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: true,
	}
	middleware := CORS(cfg)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("Origin", "https://any.example.com")

	middleware(c)

	// 通配符 + credentials 不兼容，credentials 应被禁用
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"),
		"通配符 origin 应禁用 Allow-Credentials")
}

func TestCORS_MultipleAllowedOrigins(t *testing.T) {
	cfg := config.CORSConfig{
		AllowedOrigins: []string{
			"https://app1.example.com",
			"https://app2.example.com",
		},
		AllowCredentials: false,
	}
	middleware := CORS(cfg)

	t.Run("first_origin_allowed", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Request.Header.Set("Origin", "https://app1.example.com")

		middleware(c)

		assert.Equal(t, "https://app1.example.com", w.Header().Get("Access-Control-Allow-Origin"))
		assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Headers"))
	})

	t.Run("second_origin_allowed", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Request.Header.Set("Origin", "https://app2.example.com")

		middleware(c)

		assert.Equal(t, "https://app2.example.com", w.Header().Get("Access-Control-Allow-Origin"))
		assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Headers"))
	})

	t.Run("unlisted_origin_rejected", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Request.Header.Set("Origin", "https://app3.example.com")

		middleware(c)

		assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Headers"))
	})
}

func TestCORS_VaryHeader_SetForSpecificOrigin(t *testing.T) {
	cfg := config.CORSConfig{
		AllowedOrigins:   []string{"https://allowed.example.com"},
		AllowCredentials: false,
	}
	middleware := CORS(cfg)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("Origin", "https://allowed.example.com")

	middleware(c)

	assert.Contains(t, w.Header().Values("Vary"), "Origin",
		"非通配符允许的 origin 应设置 Vary: Origin")
}

func TestNormalizeOrigins(t *testing.T) {
	tests := []struct {
		name   string
		input  []string
		expect []string
	}{
		{name: "nil_input", input: nil, expect: nil},
		{name: "empty_input", input: []string{}, expect: nil},
		{name: "trims_whitespace", input: []string{" https://a.com ", "  https://b.com"}, expect: []string{"https://a.com", "https://b.com"}},
		{name: "removes_empty_strings", input: []string{"", "  ", "https://a.com"}, expect: []string{"https://a.com"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeOrigins(tt.input)
			assert.Equal(t, tt.expect, result)
		})
	}
}

func TestCORS_ClaudeConversationPreflight(t *testing.T) {
	for _, route := range []string{"/v1/messages", "/v1/messages/count_tokens", "/v1/chat/completions", "/v1/responses"} {
		for _, allowed := range []bool{true, false} {
			origin := "https://allowed.example.com"
			if !allowed {
				origin = "https://unlisted.example.com"
			}
			t.Run(route+"/"+origin, func(t *testing.T) {
				router := gin.New()
				router.Use(CORS(config.CORSConfig{AllowedOrigins: []string{"https://allowed.example.com"}, AllowCredentials: true}))
				router.POST(route, func(c *gin.Context) { t.Error("preflight must not dispatch a model request") })
				req := httptest.NewRequest(http.MethodOptions, route, nil)
				req.Header.Set("Origin", origin)
				req.Header.Set("Access-Control-Request-Method", "POST")
				requested := []string{"authorization", "content-type", "x-sub2api-session-id", "x-claude-code-session-id", "x-claude-code-prompt-id", "anthropic-version", "anthropic-beta", "anthropic-dangerous-direct-browser-access"}
				req.Header.Set("Access-Control-Request-Headers", strings.Join(requested, ", "))
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				if !allowed {
					assert.Equal(t, http.StatusForbidden, rec.Code)
					assert.Empty(t, rec.Header().Get("Access-Control-Allow-Headers"))
					assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
					return
				}
				assert.Equal(t, http.StatusNoContent, rec.Code)
				assert.Equal(t, origin, rec.Header().Get("Access-Control-Allow-Origin"))
				assert.Equal(t, "true", rec.Header().Get("Access-Control-Allow-Credentials"))
				var names []string
				for _, name := range strings.Split(rec.Header().Get("Access-Control-Allow-Headers"), ",") {
					names = append(names, strings.ToLower(strings.TrimSpace(name)))
				}
				for _, name := range requested {
					assert.Contains(t, names, name)
				}
			})
		}
	}
}
