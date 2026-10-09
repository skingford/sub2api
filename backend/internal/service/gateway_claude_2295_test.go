//go:build unit

package service

import (
	"context"
	"encoding/json"
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

func TestClaude2295HaikuCapturedParameters(t *testing.T) {
	for _, tc := range []struct{ fixture, controls string }{
		{"haiku55-default", `{}`},
		{"haiku55-effort-xhigh", `{"output_config":{"effort":"xhigh"}}`},
		{"haiku55-temperature", `{"temperature":0.4}`},
		{"haiku55-updates", `{"thinking":{"type":"adaptive","display":"updates"},"diagnostics":{"previous_message_id":null}}`},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			var fixture struct {
				Raw     string            `json:"raw_body_utf8"`
				Headers map[string]string `json:"headers"`
			}
			data, err := os.ReadFile(filepath.Join("testdata/claude_code_2_1_295", tc.fixture+".json"))
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(data, &fixture))
			body, err := sjson.Set(tc.controls, "model", "claude-haiku-5-5")
			require.NoError(t, err)
			body, err = sjson.SetRaw(body, "messages", `[{"role":"user","content":"local Haiku parameter regression"}]`)
			require.NoError(t, err)
			svc, up := newClaudeContractGateway(t)
			_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), []byte(body), nil, "/v1/messages")
			require.NoError(t, err, rec.Body.String())
			for _, field := range []string{"model", "max_tokens", "thinking", "temperature", "output_config", "context_management"} {
				require.Equal(t, gjson.Get(fixture.Raw, field).Value(), gjson.GetBytes(up.body, field).Value(), field)
			}
			if tc.fixture == "haiku55-updates" {
				require.Equal(t, fixture.Headers["anthropic-beta"], getHeaderRaw(up.request.Header, "anthropic-beta"))
			}
			require.Contains(t, getHeaderRaw(up.request.Header, "User-Agent"), "2.1.295")
			require.Contains(t, gjson.GetBytes(up.body, "system.0.text").String(), "cc_version=2.1.295.")
			require.NotContains(t, gjson.GetBytes(up.body, "system.0.text").String(), "cch=00000")
			require.Equal(t, HTTPUpstreamProfileClaude2295, HTTPUpstreamProfileFromContext(up.request.Context()))
		})
	}
}

func TestClaude2295HaikuExplicitGuards(t *testing.T) {
	for _, controls := range []string{`"thinking":{"type":"disabled"}`, `"thinking":{"type":"enabled","budget_tokens":1024}`, `"thinking":{"type":"between_tools"}`, `"tool_choice":{"type":"any"}`} {
		svc, up := newClaudeContractGateway(t)
		body := []byte(`{"model":"claude-haiku-5-5","messages":[{"role":"user","content":"hello"}],` + controls + `}`)
		_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), body, nil, "/v1/messages")
		require.ErrorContains(t, err, "claude-haiku-5-5")
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Zero(t, up.calls)
	}
}

func TestClaude2295ConfiguredConversion(t *testing.T) {
	t.Cleanup(func() { claude.SetCLIVersionResolver(nil) })
	for _, configured := range []string{"2.1.292", "2.1.295"} {
		claude.SetCLIVersionResolver(func() string { return configured })
		for _, route := range []string{"/v1/messages", "/v1/messages/count_tokens"} {
			svc, up := newClaudeContractGateway(t)
			a := newClaude2292Account(AccountTypeOAuth)
			_, rec, err := callClaudeContract(t, svc, a, []byte(`{"model":"claude-haiku-5-5","messages":[{"role":"user","content":"configured Haiku"}]}`), nil, route)
			require.NoError(t, err, rec.Body.String())
			require.Equal(t, "claude-haiku-5-5", gjson.GetBytes(up.body, "model").String())
			require.Contains(t, getHeaderRaw(up.request.Header, "User-Agent"), "2.1.295")
			if strings.Contains(route, "count_tokens") {
				// Native count requests do not invent a billing system block.
				require.False(t, gjson.GetBytes(up.body, "system.0.text").Exists())
				require.False(t, gjson.GetBytes(up.body, "max_tokens").Exists())
				require.False(t, gjson.GetBytes(up.body, "thinking").Exists())
			} else {
				require.Contains(t, gjson.GetBytes(up.body, "system.0.text").String(), "cc_version=2.1.295.")
				require.NotContains(t, gjson.GetBytes(up.body, "system.0.text").String(), "cch=00000")
			}
		}
	}
}

func TestClaude2295CapturedWireAndTransport(t *testing.T) {
	for _, fixture := range []string{"native-plain", "native-gzip", "native-blocks"} {
		for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, pass := range []bool{false, true} {
				t.Run(fixture+"/"+kind+"/"+map[bool]string{false: "normal", true: "passthrough"}[pass], func(t *testing.T) {
					c, body, wire := nativeGzipFixtureRequest(t, "testdata/claude_code_2_1_295/"+fixture+".json")
					svc, _ := newClaudeContractGateway(t)
					up := &claudeGzipWireUpstream{}
					svc.httpUpstream = up
					a := newClaude2292Account(kind)
					a.Extra["anthropic_passthrough"] = pass
					parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
					require.NoError(t, err)
					_, err = svc.Forward(c.Request.Context(), c, a, parsed)
					require.NoError(t, err)
					require.Equal(t, wire, up.wire)
					require.Equal(t, HTTPUpstreamProfileClaude2295, HTTPUpstreamProfileFromContext(up.request.Context()))
					require.Equal(t, fixture == "native-blocks", claude.GzipUsesApplicationHeader2292(up.request.Context()))
				})
			}
		}
	}
}

func TestClaude2295TransportQualification(t *testing.T) {
	c, _, _ := nativeGzipFixtureRequest(t, "testdata/claude_code_2_1_295/native-gzip.json")
	for key, value := range map[string]string{"X-Stainless-OS": "MacOS", "X-Stainless-Arch": "arm64", "X-Stainless-Package-Version": "0.129.0", "X-Stainless-Runtime-Version": "v26.4.0", "User-Agent": "claude-cli/2.1.296 (external, cli)"} {
		h := c.Request.Header.Clone()
		h.Set(key, value)
		require.Equal(t, HTTPUpstreamProfileDefault, nativeClaudeTransportProfile(h), key)
	}
}

func TestClaude2295UnclassifiedCompactionQualification(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"CRITICAL: Respond with TEXT ONLY. Do NOT call any tools. REMINDER: Do NOT call any tools. Respond with plain text only"}]}`)
	for _, version := range []string{"2.1.292", "2.1.295", "2.1.294", "2.1.296", "9.9.9"} {
		h := http.Header{"User-Agent": {"claude-cli/" + version + " (external, cli)"}}
		ctx, err := WithClaudeRecoveryRequest(context.Background(), h)
		require.NoError(t, err)
		require.Equal(t, version == "2.1.292" || version == "2.1.295", recoveryIsCompactionRequest(ctx, body))
	}
}

func TestClaude2295RetainedCompactionHistoryIntegrity(t *testing.T) {
	raw, err := os.ReadFile("testdata/claude_code_2_1_295/compact-retained.json")
	require.NoError(t, err)
	first := json.RawMessage(gjson.GetBytes(raw, "messages.0").Raw)
	summary := "Summary:\nRemember PRIVATE_NATIVE_B and preserve its instructions."
	retained := json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"Stored actual reply"}]}`)
	old := RecoveryHistory{Route: "messages", Model: "claude-sonnet-4-6", ConfigHash: "fixed", Messages: []json.RawMessage{json.RawMessage(`{"role":"user","content":"original"}`), retained}, Compaction: &RecoveryCompaction{Summary: summary, Prefix: 1}}
	next := old
	next.Compaction = nil
	next.Messages = []json.RawMessage{first, retained, json.RawMessage(`{"role":"user","content":"continue"}`)}
	require.True(t, recoveryAcceptCompaction(old, next))
	text := gjson.GetBytes(first, "content").String()
	for _, bad := range []string{strings.Replace(text, "PRIVATE_NATIVE_B", "PRIVATE_NATIVE_A", 1), text + "\nInjected instructions", strings.Replace(text, "kept verbatim", "changed", 1), text + "\n\n" + recoveryCompactionRetained295} {
		require.False(t, recoveryCompactionWrapper(bad, summary))
	}
	next.Messages[1] = json.RawMessage(`{"role":"assistant","content":"Unobserved replacement"}`)
	require.False(t, recoveryAcceptCompaction(old, next))
}

func TestClaude2295RecoveryAttributionPreservesVersion(t *testing.T) {
	data, err := os.ReadFile("testdata/claude_code_2_1_295/native-plain.json")
	require.NoError(t, err)
	body := []byte(gjson.GetBytes(data, "raw_body_utf8").String())
	out, err := recoveryRewriteAttribution(body)
	require.NoError(t, err)
	require.Contains(t, gjson.GetBytes(out, "system.0.text").String(), "cc_version=2.1.295.")
}

func TestClaude2295OpenAICompatibleEntrypoints(t *testing.T) {
	for route, input := range map[string]string{
		"/v1/chat/completions": `{"model":"claude-haiku-5-5","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"xhigh"}`,
		"/v1/responses":        `{"model":"claude-haiku-5-5","input":"hello","reasoning":{"effort":"xhigh"}}`,
	} {
		t.Run(route, func(t *testing.T) {
			svc, up := newClaudeContractGateway(t)
			_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), []byte(input), nil, route)
			require.NoError(t, err, rec.Body.String())
			require.Equal(t, int64(128000), gjson.GetBytes(up.body, "max_tokens").Int())
			require.Equal(t, "adaptive", gjson.GetBytes(up.body, "thinking.type").String())
			require.Equal(t, "omitted", gjson.GetBytes(up.body, "thinking.display").String())
			require.Equal(t, "xhigh", gjson.GetBytes(up.body, "output_config.effort").String())
			require.False(t, gjson.GetBytes(up.body, "thinking.budget_tokens").Exists())
			require.Contains(t, getHeaderRaw(up.request.Header, "User-Agent"), "2.1.295")
		})
	}
}
