//go:build unit

package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestClaudeNativeGzipVariants(t *testing.T) {
	files, err := filepath.Glob("testdata/claude_code_2_1_292/gzip_alignment/*.json")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, path := range files {
		for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, passthrough := range []bool{false, true} {
				name := strings.TrimSuffix(filepath.Base(path), ".json") + "-" + kind + "-" + map[bool]string{false: "normal", true: "passthrough"}[passthrough]
				t.Run(name, func(t *testing.T) {
					c, body, wire := nativeGzipFixtureRequest(t, path)
					svc, _ := newClaudeContractGateway(t)
					up := &claudeGzipWireUpstream{}
					svc.httpUpstream = up
					account := newClaude2292Account(kind)
					account.Extra["anthropic_passthrough"] = passthrough
					parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
					require.NoError(t, err)
					if strings.Contains(c.Request.URL.Path, "/count_tokens") {
						err = svc.ForwardCountTokens(c.Request.Context(), c, account, parsed)
					} else {
						_, err = svc.Forward(c.Request.Context(), c, account, parsed)
					}
					require.NoError(t, err)
					require.Equal(t, 1, up.calls)
					require.Equal(t, wire, up.wire)
					require.Equal(t, []string{"gzip"}, up.request.Header["Content-Encoding"])
					require.Equal(t, int64(len(wire)), up.request.ContentLength)
					retry, err := up.request.GetBody()
					require.NoError(t, err)
					again, err := io.ReadAll(retry)
					require.NoError(t, err)
					require.NoError(t, retry.Close())
					require.Equal(t, wire, again)
					data, err := os.ReadFile(path)
					require.NoError(t, err)
					application := gjson.GetBytes(data, "application_encoding").Bool()
					require.Equal(t, application, claude.GzipUsesApplicationHeader2292(up.request.Context()))
					require.Equal(t, HTTPUpstreamProfileClaude2292, HTTPUpstreamProfileFromContext(up.request.Context()))
					if folder := os.Getenv("CLAUDE_GZIP_FIX_EXPORT"); folder != "" {
						require.NoError(t, os.MkdirAll(folder, 0700))
						headers := map[string]string{}
						for k, v := range up.request.Header {
							require.Len(t, v, 1)
							headers[k] = v[0]
						}
						row := map[string]any{"method": up.request.Method, "path": up.request.URL.RequestURI(), "headers": headers, "raw_body_utf8": string(body), "wire_body_base64": base64.StdEncoding.EncodeToString(wire), "upstream_profile": HTTPUpstreamProfileFromContext(up.request.Context()), "synthetic_auth": kind, "application_encoding": claude.GzipUsesApplicationHeader2292(up.request.Context())}
						b, err := json.MarshalIndent(row, "", "  ")
						require.NoError(t, err)
						require.NoError(t, os.WriteFile(filepath.Join(folder, name+".json"), append(b, '\n'), 0600))
					}
				})
			}
		}
	}
}

func TestClaudeNativeGzipLegacyEncodingOverrides(t *testing.T) {
	for _, name := range []string{"Content-Encoding", "content-encoding", "CONTENT-ENCODING", "cOnTeNt-EnCoDiNg"} {
		for _, value := range []string{"gzip", "identity", "br"} {
			for _, mapped := range []bool{false, true} {
				t.Run(name+"/"+value+"/"+map[bool]string{false: "unchanged", true: "mapped"}[mapped], func(t *testing.T) {
					c, body, wire := nativeGzipRequest(t)
					svc, _ := newClaudeContractGateway(t)
					up := &claudeGzipWireUpstream{}
					svc.httpUpstream = up
					account := newClaude2292Account(AccountTypeAPIKey)
					account.Credentials[credKeyHeaderOverrideEnabled] = true
					account.Credentials[credKeyHeaderOverrides] = map[string]any{name: value}
					require.Error(t, NormalizeHeaderOverrideCredentials(account.Credentials))
					require.Empty(t, account.GetHeaderOverrides())
					if mapped {
						account.Credentials["model_mapping"] = map[string]any{"claude-sonnet-4-6": "claude-opus-4-6"}
					}
					parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
					require.NoError(t, err)
					_, err = svc.Forward(c.Request.Context(), c, account, parsed)
					require.NoError(t, err)
					require.Equal(t, 1, up.calls)
					variants := 0
					for k := range up.request.Header {
						if strings.EqualFold(k, "content-encoding") {
							variants++
						}
					}
					if mapped {
						require.Zero(t, variants)
						require.True(t, gjson.ValidBytes(up.wire))
						require.Equal(t, "claude-opus-4-6", gjson.GetBytes(up.wire, "model").String())
					} else {
						require.Equal(t, 1, variants)
						require.Equal(t, wire, up.wire)
					}
				})
			}
		}
	}
}

func TestClaudeNativeCountGzipChangedBodyAndScope(t *testing.T) {
	for _, scenario := range []string{"changed", "custom-origin", "unknown-version", "disabled-preservation", "unvalidated"} {
		t.Run(scenario, func(t *testing.T) {
			c, body, wire := nativeGzipFixtureRequest(t, "testdata/claude_code_2_1_292/gzip_alignment/gzip-count-3.json")
			a := newClaude2292Account(AccountTypeAPIKey)
			target := "https://api.anthropic.com/v1/messages/count_tokens?beta=true"
			if scenario == "custom-origin" {
				target = "https://relay.example/v1/messages/count_tokens"
			}
			if scenario == "changed" {
				var err error
				body, err = sjson.SetBytes(body, "model", "claude-opus-4-6")
				require.NoError(t, err)
			}
			if scenario == "disabled-preservation" {
				a.Extra["claude_native_passthrough"] = false
			}
			if scenario == "unvalidated" {
				c.Request = c.Request.WithContext(SetClaudeCodeClient(c.Request.Context(), false))
			}
			req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, target, bytes.NewReader(body))
			require.NoError(t, err)
			req.Header = c.Request.Header.Clone()
			if scenario == "unknown-version" {
				req.Header.Set("User-Agent", "claude-cli/9.9.9 (external, sdk-cli)")
			}
			// Even stale raw headers cannot claim compression for decoded/changed JSON.
			req.Header["content-encoding"] = []string{"gzip"}
			req.Header["CONTENT-ENCODING"] = []string{"identity"}
			out, err := finalizeNativeClaudeRequest(req, c, a, body)
			require.NoError(t, err)
			actual, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			if scenario == "unknown-version" {
				require.Equal(t, wire, actual)
				require.Equal(t, "gzip", getHeaderRaw(req.Header, "Content-Encoding"))
				require.Equal(t, body, out)
				require.NotContains(t, req.Header, "content-encoding")
				require.NotContains(t, req.Header, "CONTENT-ENCODING")
			} else {
				for key := range req.Header {
					require.False(t, strings.EqualFold(key, "content-encoding"))
				}
				require.Equal(t, out, actual)
			}
			require.False(t, claude.GzipUsesApplicationHeader2292(req.Context()))
		})
	}
}

func TestHeaderRawEncodingCaseVariants(t *testing.T) {
	h := http.Header{"cOnTeNt-EnCoDiNg": []string{"gzip"}}
	require.Equal(t, "gzip", getHeaderRaw(h, "Content-Encoding"))
	h["content-encoding"] = []string{"br"}
	h["Content-Encoding"] = []string{"identity"}
	setHeaderRaw(h, "Content-Encoding", "gzip")
	require.Equal(t, http.Header{"Content-Encoding": []string{"gzip"}}, h)
	h["CONTENT-ENCODING"] = []string{"identity"}
	deleteHeaderAllForms(h, "content-encoding")
	require.Empty(t, h)
}
