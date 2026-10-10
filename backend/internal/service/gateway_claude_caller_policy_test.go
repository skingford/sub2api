//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Expected layout is copied from independent, pinned CLI captures, not from
// this request builder. Attribution/counters have a separate native contract.
func TestClaudeCallerCustomSystemMatchesCapturedLayout(t *testing.T) {
	data, err := os.ReadFile("testdata/claude_cli_custom_system_profiles.json")
	require.NoError(t, err)
	var fixtures []struct {
		Version      string          `json:"version"`
		Mode         string          `json:"mode"`
		CallerSystem string          `json:"caller_system"`
		Expected     json.RawMessage `json:"expected_system_after_attribution"`
		UserAgent    string          `json:"user_agent"`
	}
	require.NoError(t, json.Unmarshal(data, &fixtures))
	require.Len(t, fixtures, 4)
	defer claude.SetCLIVersionResolver(nil)
	for _, fixture := range fixtures {
		for _, route := range []string{"/v1/messages", "/v1/chat/completions", "/v1/responses"} {
			t.Run(fixture.Version+"/"+fixture.Mode+route, func(t *testing.T) {
				claude.SetCLIVersionResolver(func() string { return fixture.Version })
				svc, up := newClaudeContractGateway(t)
				svc.cfg.Gateway.ClaudeOAuthPreserveCaller = true
				input := map[string]any{"model": "claude-sonnet-4-6"}
				switch route {
				case "/v1/messages":
					input["messages"] = []any{map[string]any{"role": "user", "content": "hi"}}
					input["system"] = fixture.CallerSystem
				case "/v1/chat/completions":
					input["messages"] = []any{map[string]any{"role": "user", "content": "hi"}}
					if fixture.CallerSystem != "" {
						input["messages"] = []any{map[string]any{"role": "system", "content": fixture.CallerSystem}, map[string]any{"role": "user", "content": "hi"}}
					}
				default:
					input["input"] = "hi"
					input["instructions"] = fixture.CallerSystem
				}
				payload, e := json.Marshal(input)
				require.NoError(t, e)
				_, _, e = callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), payload, nil, route)
				require.NoError(t, e)
				blocks := gjson.GetBytes(up.body, "system").Array()
				require.GreaterOrEqual(t, len(blocks), 2)
				actual := make([]json.RawMessage, 0, len(blocks)-1)
				for _, block := range blocks[1:] {
					actual = append(actual, json.RawMessage(block.Raw))
				}
				encoded, e := json.Marshal(actual)
				require.NoError(t, e)
				require.JSONEq(t, string(fixture.Expected), string(encoded))
				require.Equal(t, fixture.UserAgent, getHeaderRaw(up.request.Header, "User-Agent"))
				require.Contains(t, blocks[0].Get("text").String(), "cc_entrypoint=cli;")
			})
		}
	}
}

func TestClaudeCallerPolicySystemBlocks(t *testing.T) {
	svc := &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{ClaudeOAuthPreserveCaller: true}}}
	for _, input := range []string{
		`"  You are OpenCode, the best coding agent on the planet.\n\n中文  "`,
		`[{"type":"text","text":"first","cache_control":{"type":"ephemeral","ttl":"1h"},"extra":{"integer":9007199254740993}},{"text":"second","type":"text","cache_control":{"type":"ephemeral","ttl":"5m"}}]`,
		`"You are Claude Code, with my own instructions."`, `""`, `null`, `[]`,
	} {
		t.Run(input, func(t *testing.T) {
			body := []byte(`{"system":` + input + `,"messages":[{"role":"user","content":"hello"}],"tools":[]}`)
			out, err := svc.rewriteClaudeOAuthSystem(context.Background(), nil, body, nil, "claude-sonnet-4-6")
			require.NoError(t, err)
			require.Equal(t, gjson.GetBytes(body, "messages").Raw, gjson.GetBytes(out, "messages").Raw)
			blocks := gjson.GetBytes(out, "system").Array()
			require.GreaterOrEqual(t, len(blocks), 2)
			require.Contains(t, blocks[0].Get("text").String(), "x-anthropic-billing-header:")
			require.Equal(t, claudeCodeSystemPrompt, blocks[1].Get("text").String())
			original := gjson.Parse(input)
			switch {
			case original.IsArray():
				require.Len(t, blocks, 2+len(original.Array()))
				for i, block := range original.Array() {
					require.Equal(t, block.Raw, blocks[i+2].Raw)
				}
			case original.Type == gjson.String && original.String() != "":
				require.Len(t, blocks, 3)
				require.Equal(t, original.String(), blocks[2].Get("text").String())
			default:
				require.Len(t, blocks, 2)
			}
			require.NotContains(t, string(out), "[System Instructions]")
			require.NotContains(t, string(out), "Understood. I will follow these instructions.")
			require.NotContains(t, string(out), claudeCodeSystemPromptExpansion)
		})
	}
	for _, invalid := range []string{`42`, `{}`, `["instruction"]`, `[{"type":"image"}]`, `[{"type":"text","text":42}]`} {
		_, err := svc.rewriteClaudeOAuthSystem(context.Background(), nil, []byte(`{"system":`+invalid+`}`), nil, "claude-sonnet-4-6")
		require.Error(t, err, invalid)
	}
}

func TestClaudeCallerPolicyMessagesAndCount(t *testing.T) {
	// Four caller breakpoints span two system blocks, a message and a tool.
	// Six tools exercise both static and dynamic legacy rename rules.
	const input = `{"model":"claude-sonnet-4-6","thinking":{"type":"disabled"},"system":[{"type":"text","text":"You are OpenCode, the best coding agent on the planet.","cache_control":{"type":"ephemeral","ttl":"5m"}},{"type":"text","text":"  Project instructions\n","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","content":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral","ttl":"5m"}}]},{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"sessions_list","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"done"}]}],"tools":[{"name":"sessions_list","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral","ttl":"5m"}},{"name":"a","input_schema":{"type":"object"}},{"name":"b","input_schema":{"type":"object"}},{"name":"c","input_schema":{"type":"object"}},{"name":"d","input_schema":{"type":"object"}},{"name":"e","input_schema":{"type":"object"}}],"tool_choice":{"type":"tool","name":"sessions_list"}}`
	for _, route := range []string{"/v1/messages", "/v1/messages/count_tokens"} {
		t.Run(route, func(t *testing.T) {
			svc, up := newClaudeContractGateway(t)
			svc.cfg.Gateway.ClaudeOAuthPreserveCaller = true
			svc.settingService = NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{
				SettingKeyRewriteMessageCacheControl:         "true",
				SettingKeyEnableAnthropicCacheTTL1hInjection: "true",
				SettingKeyEnableClientDatelineNormalization:  "true",
				SettingKeyClaudeOAuthSystemPrompt:            "LEGACY_CUSTOM_EXPANSION",
				SettingKeyClaudeOAuthSystemPromptBlocks:      `[{"text":"LEGACY_CUSTOM_BLOCK"}]`,
			}}, svc.cfg)
			c, _, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), []byte(input), nil, route)
			require.NoError(t, err)
			require.Equal(t, 1, up.calls)
			for _, field := range []string{"messages", "tools", "tool_choice"} {
				if route == "/v1/messages/count_tokens" && field == "tool_choice" {
					continue // count_tokens has no generation choice contract.
				}
				require.JSONEq(t, gjson.Get(input, field).Raw, gjson.GetBytes(up.body, field).Raw, field)
			}
			require.NotContains(t, string(up.body), "LEGACY_CUSTOM")
			_, renamed := c.Get(toolNameRewriteKey)
			require.False(t, renamed)
			if route == "/v1/messages/count_tokens" {
				require.Equal(t, gjson.Get(input, "system").Raw, gjson.GetBytes(up.body, "system").Raw)
				return
			}
			blocks := gjson.GetBytes(up.body, "system").Array()
			require.Len(t, blocks, 4)
			for i, block := range gjson.Get(input, "system").Array() {
				require.Equal(t, block.Raw, blocks[i+2].Raw)
			}
		})
	}
}

func TestClaudeCallerCountDoesNotAddSystem(t *testing.T) {
	for _, system := range []string{"", `,"system":"count only this"`, `,"system":[{"type":"text","text":"component","cache_control":{"type":"ephemeral","ttl":"1h"}}]`} {
		svc, up := newClaudeContractGateway(t)
		svc.cfg.Gateway.ClaudeOAuthPreserveCaller = true
		input := []byte(`{"model":"claude-sonnet-4-6","tools":[],"messages":[{"role":"user","content":"foo"}]` + system + `}`)
		_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), input, nil, "/v1/messages/count_tokens")
		require.NoError(t, err)
		require.Equal(t, 200, rec.Code)
		require.Equal(t, 1, up.calls)
		require.JSONEq(t, string(input), string(up.body))
	}
}

func TestClaudeCallerPolicyRejectsCacheLossBeforeDispatch(t *testing.T) {
	for _, route := range []string{"/v1/messages", "/v1/messages/count_tokens"} {
		for _, scenario := range []string{"too_many", "thinking"} {
			t.Run(route+"/"+scenario, func(t *testing.T) {
				svc, up := newClaudeContractGateway(t)
				svc.cfg.Gateway.ClaudeOAuthPreserveCaller = true
				blocks := []map[string]any{}
				for i := 0; i < 5; i++ {
					blocks = append(blocks, map[string]any{"type": "text", "text": fmt.Sprint(i), "cache_control": map[string]string{"type": "ephemeral"}})
				}
				body, err := json.Marshal(map[string]any{"model": "claude-sonnet-4-6", "messages": []any{map[string]any{"role": "user", "content": blocks}}})
				require.NoError(t, err)
				if scenario == "thinking" {
					body = []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"local test","signature":"synthetic-signature","cache_control":{"type":"ephemeral"}}]},{"role":"user","content":"continue"}]}`)
				}
				_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), body, nil, route)
				require.Error(t, err)
				require.Equal(t, 400, rec.Code)
				require.Zero(t, up.calls)
			})
		}
	}
}

func TestClaudeCallerPolicyInjectionDisabled(t *testing.T) {
	svc, up := newClaudeContractGateway(t)
	svc.cfg.Gateway.ClaudeOAuthPreserveCaller = true
	svc.settingService = NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{SettingKeyEnableClaudeOAuthSystemPromptInjection: "false"}}, svc.cfg)
	input := []byte(`{"model":"claude-sonnet-4-6","system":"You are OpenCode, the best coding agent on the planet.","messages":[{"role":"user","content":"hello"}]}`)
	_, _, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), input, nil, "/v1/messages")
	require.NoError(t, err)
	require.Equal(t, gjson.GetBytes(input, "system").Raw, gjson.GetBytes(up.body, "system").Raw)
	require.Equal(t, gjson.GetBytes(input, "messages").Raw, gjson.GetBytes(up.body, "messages").Raw)
}

func TestClaudeCallerPolicyNativeUnchanged(t *testing.T) {
	for _, fixture := range claude2292ValidationFixtures {
		t.Run(fixture, func(t *testing.T) {
			svc := newClaude2292Gateway()
			svc.cfg.Gateway.ClaudeOAuthPreserveCaller = true
			capture := loadNativeClaudeCapture(t, "2_1_292", fixture)
			_, body := forwardClaude2292Capture(t, svc, newClaude2292Account(AccountTypeOAuth), capture)
			require.Equal(t, []byte(capture.Body), body)
		})
	}
}

func TestClaudeCallerPolicyAdapterToolNames(t *testing.T) {
	for _, route := range []string{"/v1/chat/completions", "/v1/responses"} {
		t.Run(route, func(t *testing.T) {
			svc, up := newClaudeContractGateway(t)
			svc.cfg.Gateway.ClaudeOAuthPreserveCaller = true
			names := []string{"sessions_list", "session_open", "read", "write", "search", "close"}
			tools := make([]any, 0, len(names))
			for _, name := range names {
				function := map[string]any{"name": name, "parameters": map[string]any{"type": "object"}}
				if route == "/v1/chat/completions" {
					tools = append(tools, map[string]any{"type": "function", "function": function})
				} else {
					function["type"] = "function"
					tools = append(tools, function)
				}
			}
			input := map[string]any{"model": "claude-sonnet-4-6", "tools": tools}
			if route == "/v1/chat/completions" {
				input["messages"] = []any{
					map[string]any{"role": "system", "content": "Keep my tool names"},
					map[string]any{"role": "user", "content": "list"},
					map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": names[0], "arguments": "{}"}}}},
					map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "done"},
				}
			} else {
				input["instructions"] = "Keep my tool names"
				input["input"] = []any{
					map[string]any{"role": "user", "content": "list"},
					map[string]any{"type": "function_call", "call_id": "call_1", "name": names[0], "arguments": "{}"},
					map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "done"},
				}
			}
			payload, err := json.Marshal(input)
			require.NoError(t, err)
			c, _, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), payload, nil, route)
			require.NoError(t, err)
			require.Equal(t, 1, up.calls)
			_, renamed := c.Get(toolNameRewriteKey)
			require.False(t, renamed)
			actual := gjson.GetBytes(up.body, "tools").Array()
			require.Len(t, actual, len(names))
			for i, tool := range actual {
				require.Equal(t, names[i], tool.Get("name").String())
				require.False(t, tool.Get("cache_control").Exists())
			}
			require.Equal(t, names[0], gjson.GetBytes(up.body, "messages.1.content.0.name").String())
			require.Len(t, gjson.GetBytes(up.body, "messages").Array(), 3)
		})
	}
}

func TestClaudeCallerPolicyRefusalIsNotRetried(t *testing.T) {
	for _, route := range []string{"/v1/messages", "/v1/chat/completions", "/v1/responses"} {
		t.Run(route, func(t *testing.T) {
			svc, up := newClaudeContractGateway(t)
			svc.cfg.Gateway.ClaudeOAuthPreserveCaller = true
			up.status = 403
			body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}]}`)
			if route == "/v1/responses" {
				var err error
				body, err = sjson.SetBytes(body, "input", "hi")
				require.NoError(t, err)
			}
			_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), body, nil, route)
			require.Error(t, err)
			require.Equal(t, 403, rec.Code)
			require.Equal(t, 1, up.calls)
		})
	}
}
