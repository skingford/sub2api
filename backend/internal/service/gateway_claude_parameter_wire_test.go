//go:build unit

package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	requestbody "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestClaudeParameterAlignmentNativeControls(t *testing.T) {
	for _, model := range []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5-20251001", "claude-opus-5-5"} {
		for _, variant := range []string{"thinking-off", "disabled-control-extra", "temperature"} {
			if model == "claude-opus-5-5" && variant != "temperature" {
				continue
			}
			t.Run(model+"/"+variant, func(t *testing.T) {
				var fixture struct {
					Case struct {
						Controls map[string]any `json:"generic_controls"`
					} `json:"case"`
					Requests []struct {
						Raw string `json:"raw_body_utf8"`
					} `json:"requests"`
				}
				data, e := os.ReadFile(filepath.Join("testdata/claude_code_2_1_292/parameter_alignment", model+"-"+variant+".json"))
				require.NoError(t, e)
				require.NoError(t, json.Unmarshal(data, &fixture))
				require.Len(t, fixture.Requests, 1)
				input := fixture.Case.Controls
				input["model"] = model
				input["messages"] = []any{map[string]any{"role": "user", "content": "local parameter alignment"}}
				body, e := json.Marshal(input)
				require.NoError(t, e)
				svc, up := newClaudeContractGateway(t)
				_, _, e = callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), body, nil, "/v1/messages")
				require.NoError(t, e)
				require.Equal(t, 1, up.calls)
				for _, field := range []string{"model", "max_tokens", "temperature", "thinking", "output_config", "context_management"} {
					require.Equal(t, gjson.Get(fixture.Requests[0].Raw, field).Value(), gjson.GetBytes(up.body, field).Value(), field)
				}
				if strings.Contains(variant, "disabled") {
					require.NotContains(t, getHeaderRaw(up.request.Header, "anthropic-beta"), "thinking-display-updates")
				}
			})
		}
	}
}

func TestClaudeParameterAlignmentPreservesExplicitChoicesAndGuards(t *testing.T) {
	input := []byte(`{"model":"claude-sonnet-4-6","max_tokens":2048,"thinking":{"type":"disabled","display":"updates","unknown":"remove"},"temperature":0.2,"output_config":{"effort":"low"},"context_management":{"edits":[]},"messages":[{"role":"user","content":"hi"}]}`)
	out, _ := normalizeClaudeOAuthRequestBody(input, "claude-sonnet-4-6", claudeOAuthNormalizeOptions{})
	require.JSONEq(t, `{"type":"disabled"}`, gjson.GetBytes(out, "thinking").Raw)
	for _, key := range []string{"max_tokens", "temperature", "output_config", "context_management"} {
		require.Equal(t, gjson.GetBytes(input, key).Raw, gjson.GetBytes(out, key).Raw, key)
	}
	for _, model := range []string{"claude-sonnet-5-5", "claude-opus-5-5"} {
		svc, up := newClaudeContractGateway(t)
		body, e := sjson.SetBytes(input, "model", model)
		require.NoError(t, e)
		_, rec, e := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), body, nil, "/v1/messages")
		require.Error(t, e)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Zero(t, up.calls)
	}
	count, _ := normalizeClaudeOAuthRequestBody([]byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}]}`), "claude-sonnet-4-6", claudeOAuthNormalizeOptions{countTokens: true})
	for _, key := range []string{"temperature", "thinking", "max_tokens", "output_config"} {
		require.False(t, gjson.GetBytes(count, key).Exists(), key)
	}
}

type claudeGzipWireUpstream struct {
	request *http.Request
	wire    []byte
	calls   int
}

func (u *claudeGzipWireUpstream) Do(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
	u.calls++
	u.request = req
	var err error
	u.wire, err = io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	logical := u.wire
	if strings.EqualFold(getHeaderRaw(req.Header, "Content-Encoding"), "gzip") {
		r, e := gzip.NewReader(bytes.NewReader(u.wire))
		if e != nil {
			return nil, e
		}
		logical, e = io.ReadAll(r)
		_ = r.Close()
		if e != nil {
			return nil, e
		}
	}
	responseRequest := req.Clone(req.Context())
	responseRequest.Body = io.NopCloser(bytes.NewReader(logical))
	deleteHeaderAllForms(responseRequest.Header, "Content-Encoding")
	return (&claudeContractUpstream{}).Do(responseRequest, proxy, id, concurrency)
}
func (u *claudeGzipWireUpstream) DoWithTLS(req *http.Request, p string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, p, id, n)
}

func nativeGzipRequest(t *testing.T) (*gin.Context, []byte, []byte) {
	t.Helper()
	return nativeGzipFixtureRequest(t, "testdata/claude_code_2_1_292/parameter_alignment/native-gzip.json")
}

func nativeGzipFixtureRequest(t *testing.T, path string) (*gin.Context, []byte, []byte) {
	t.Helper()
	var fixture struct {
		Path    string            `json:"path"`
		Headers map[string]string `json:"headers"`
		Raw     string            `json:"raw_body_utf8"`
		Wire    string            `json:"wire_body_base64"`
		Digest  string            `json:"decoded_sha256"`
	}
	data, e := os.ReadFile(path)
	require.NoError(t, e)
	require.NoError(t, json.Unmarshal(data, &fixture))
	wire, e := base64.StdEncoding.DecodeString(fixture.Wire)
	require.NoError(t, e)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", fixture.Path, bytes.NewReader(wire))
	for k, v := range fixture.Headers {
		c.Request.Header.Set(k, v)
	}
	body, e := requestbody.ReadRequestBodyWithPrealloc(c.Request)
	require.NoError(t, e)
	if fixture.Digest != "" {
		require.Equal(t, fixture.Digest, recoveryHash(body))
	} else {
		require.Equal(t, fixture.Raw, string(body))
	}
	// The group model allowlist prereads and resets the body before the handler.
	c.Request.Body = requestbody.NewPrereadBody(body)
	decoded, e := requestbody.ReadRequestBodyWithPrealloc(c.Request)
	require.NoError(t, e)
	require.Equal(t, body, decoded)
	var values map[string]any
	require.NoError(t, json.Unmarshal(body, &values))
	validated := NewClaudeCodeValidator().Validate(c.Request, values)
	require.True(t, validated)
	c.Request = c.Request.WithContext(SetClaudeCodeClient(c.Request.Context(), validated))
	return c, body, wire
}

func TestClaudeNativeGzipWireParity(t *testing.T) {
	for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
		for _, passthrough := range []bool{false, true} {
			t.Run(kind+"/"+map[bool]string{false: "normal", true: "passthrough"}[passthrough], func(t *testing.T) {
				c, body, wire := nativeGzipRequest(t)
				svc, _ := newClaudeContractGateway(t)
				up := &claudeGzipWireUpstream{}
				svc.httpUpstream = up
				a := newClaude2292Account(kind)
				a.Extra["anthropic_passthrough"] = passthrough
				parsed, e := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
				require.NoError(t, e)
				_, e = svc.Forward(c.Request.Context(), c, a, parsed)
				require.NoError(t, e)
				require.Equal(t, 1, up.calls)
				require.Equal(t, wire, up.wire)
				require.Equal(t, "gzip", getHeaderRaw(up.request.Header, "Content-Encoding"))
				require.Equal(t, int64(len(wire)), up.request.ContentLength)
				retry, e := up.request.GetBody()
				require.NoError(t, e)
				again, e := io.ReadAll(retry)
				require.NoError(t, e)
				require.NoError(t, retry.Close())
				require.Equal(t, wire, again)
				require.Equal(t, HTTPUpstreamProfileClaude2292, HTTPUpstreamProfileFromContext(up.request.Context()))
				if folder := os.Getenv("CLAUDE_WIRE_ALIGNMENT_EXPORT"); folder != "" {
					require.NoError(t, os.MkdirAll(folder, 0700))
					headers := map[string]string{}
					for k, v := range up.request.Header {
						headers[k] = v[0]
					}
					row := map[string]any{"method": up.request.Method, "path": up.request.URL.RequestURI(), "headers": headers, "raw_body_utf8": string(body), "wire_body_base64": base64.StdEncoding.EncodeToString(up.wire), "upstream_profile": HTTPUpstreamProfileFromContext(up.request.Context()), "synthetic_auth": kind}
					b, e := json.MarshalIndent(row, "", "  ")
					require.NoError(t, e)
					require.NoError(t, os.WriteFile(filepath.Join(folder, kind+"-"+map[bool]string{false: "normal", true: "passthrough"}[passthrough]+".json"), append(b, '\n'), 0600))
				}
			})
		}
	}
}

func TestClaudeNativeGzipNeverRestoresChangedBody(t *testing.T) {
	c, body, wire := nativeGzipRequest(t)
	svc, _ := newClaudeContractGateway(t)
	a := newClaude2292Account(AccountTypeOAuth)
	ctx := withNativeClaudeBodyIntegrity(c.Request.Context(), c, a, body)
	changed, e := sjson.SetBytes(body, "model", "claude-opus-4-6")
	require.NoError(t, e)
	req, final, e := svc.buildUpstreamRequest(ctx, c, a, changed, "local-only-oauth-token-not-a-real-credential", "oauth", "claude-opus-4-6", true, false)
	require.NoError(t, e)
	require.Empty(t, getHeaderRaw(req.Header, "Content-Encoding"))
	actual, e := io.ReadAll(req.Body)
	require.NoError(t, e)
	require.Equal(t, final, actual)
	require.NotEqual(t, wire, actual)
	require.Equal(t, "claude-opus-4-6", gjson.GetBytes(actual, "model").String())
	require.NotContains(t, gjson.GetBytes(actual, "system.0.text").String(), "cch=00000;")
	// Recovery/identity rewrites have the same digest guard; no account-scoped cache.
	other := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(body)).WithContext(context.Background())
	_, _, ok := requestbody.OriginalRequestEncoding(other, body)
	require.False(t, ok)
}

func TestClaudeNativeGzipHonorsScopeAndHeaderPolicy(t *testing.T) {
	for _, scenario := range []string{"custom-origin", "unknown-version", "disabled-preservation"} {
		t.Run(scenario, func(t *testing.T) {
			c, body, wire := nativeGzipRequest(t)
			a := newClaude2292Account(AccountTypeOAuth)
			ctx := withNativeClaudeBodyIntegrity(c.Request.Context(), c, a, body)
			target := "https://api.anthropic.com/v1/messages?beta=true"
			if scenario == "custom-origin" {
				target = "https://relay.example/v1/messages"
			}
			req, e := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
			require.NoError(t, e)
			req.Header = c.Request.Header.Clone()
			switch scenario {
			case "unknown-version":
				req.Header.Set("User-Agent", "claude-cli/9.9.9 (external, sdk-cli)")
			case "disabled-preservation":
				a.Extra["claude_native_passthrough"] = false
			}
			out, e := finalizeNativeClaudeRequest(req, c, a, body)
			require.NoError(t, e)
			actual, e := io.ReadAll(req.Body)
			require.NoError(t, e)
			if scenario == "unknown-version" {
				require.Equal(t, "gzip", getHeaderRaw(req.Header, "Content-Encoding"))
				require.Equal(t, wire, actual)
				require.Equal(t, body, out)
			} else {
				require.NotEqual(t, "gzip", getHeaderRaw(req.Header, "Content-Encoding"))
				require.Equal(t, out, actual)
				if scenario == "disabled-preservation" {
					expected, err := finalizeClaude2292Billing(body, gjson.GetBytes(body, "system.0.text"))
					require.NoError(t, err)
					require.Equal(t, expected, actual)
				}
			}
		})
	}
}
