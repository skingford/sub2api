package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnthropicParallelToolConstraints(t *testing.T) {
	for _, choice := range []string{"", "null", `"auto"`, `"none"`, `"required"`, `{"type":"function","name":"lookup"}`, `{"type":"function","function":{"name":"lookup"}}`} {
		for _, parallel := range []bool{false, true} {
			req := &ResponsesRequest{Model: "claude-haiku-4-5-20251001", Input: json.RawMessage(`"hi"`), Tools: []ResponsesTool{{Type: "function", Name: "lookup"}}, ToolChoice: json.RawMessage(choice), ParallelToolCalls: &parallel}
			out, err := ResponsesToAnthropicRequest(req)
			require.NoError(t, err)
			var got map[string]any
			require.NoError(t, json.Unmarshal(out.ToolChoice, &got))
			if choice == `"none"` {
				require.Equal(t, map[string]any{"type": "none"}, got)
			} else {
				require.Equal(t, !parallel, got["disable_parallel_tool_use"])
				if choice == `"required"` {
					require.Equal(t, "any", got["type"])
				} else if choice != "" && choice[0] == '{' {
					require.Equal(t, "tool", got["type"])
					require.Equal(t, "lookup", got["name"])
				} else {
					require.Equal(t, "auto", got["type"])
				}
			}
		}
	}
	parallel := false
	out, err := ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-sonnet-4-6", Input: json.RawMessage(`"hi"`), ParallelToolCalls: &parallel})
	require.NoError(t, err)
	require.Empty(t, out.ToolChoice, "a vacuous setting must not introduce tool_choice without tools")
	for _, raw := range []string{`"future"`, `{"type":"future"}`, `[]`} {
		_, err = anthropicParallelToolChoice(json.RawMessage(raw), false)
		require.Error(t, err)
	}
}

func TestAnthropicStrictToolPreservesSchemaAndTriState(t *testing.T) {
	// Top-level unions are deliberately flattened by the legacy non-strict
	// adapter. An explicit strict schema must retain its original constraints.
	schema := json.RawMessage(`{"type":"object","properties":{"n":{"type":"integer","const":9007199254740993}},"oneOf":[{"required":["n"]},{"properties":{"n":{"const":0}}}],"additionalProperties":false}`)
	for _, kind := range []string{"function", "custom"} {
		for _, value := range []*bool{nil, new(bool), func() *bool { b := true; return &b }()} {
			out, err := ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-sonnet-4-6", Input: json.RawMessage(`"hi"`), Tools: []ResponsesTool{{Type: kind, Name: "lookup", Parameters: schema, Strict: value}}})
			require.NoError(t, err)
			require.Equal(t, value, out.Tools[0].Strict)
			wire, err := json.Marshal(out.Tools[0])
			require.NoError(t, err)
			if value == nil {
				require.NotContains(t, string(wire), `"strict"`)
			} else if *value {
				require.Equal(t, string(schema), string(out.Tools[0].InputSchema))
				require.Contains(t, string(wire), `9007199254740993`)
				require.Contains(t, string(wire), `"strict":true`)
			} else {
				require.Contains(t, string(wire), `"strict":false`)
			}
		}
	}
	strict := true
	for _, schema := range []string{"", "null", "[]", `{"type":"string"}`, `{"oneOf":[{"type":"object"}]}`} {
		_, err := ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-sonnet-4-6", Input: json.RawMessage(`"hi"`), Tools: []ResponsesTool{{Type: "function", Name: "bad", Parameters: json.RawMessage(schema), Strict: &strict}}})
		require.ErrorContains(t, err, "object input schema")
	}
	_, err := ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-sonnet-4-6", Input: json.RawMessage(`"hi"`), Tools: []ResponsesTool{{Type: "web_search", Strict: &strict}}})
	require.ErrorContains(t, err, "server tool")
}

func TestAnthropicLegacyReasoningRespectsIntentAndCap(t *testing.T) {
	for _, model := range []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5-20251001"} {
		for _, cap := range []int{1, 64, 1024, 1025, 4096, 16384, 128000} {
			for _, effort := range []string{"none", "low", "medium", "high", "xhigh", "max"} {
				req := &ResponsesRequest{Model: model, Input: json.RawMessage(`"hi"`), MaxOutputTokens: &cap, Reasoning: &ResponsesReasoning{Effort: effort}, Text: &ResponsesText{Format: json.RawMessage(`{"type":"json_schema","schema":` + constraintSchema + `}`)}}
				out, err := ResponsesToAnthropicRequest(req)
				if model == "claude-haiku-4-5-20251001" && cap <= 1024 && effort != "none" && effort != "low" {
					require.ErrorContains(t, err, "greater than 1024")
					continue
				}
				require.NoError(t, err)
				require.Equal(t, cap, out.MaxTokens)
				require.NotEmpty(t, out.OutputConfig.Format)
				if effort == "none" {
					require.Equal(t, "disabled", out.Thinking.Type)
					require.Zero(t, out.Thinking.BudgetTokens)
					require.Empty(t, out.OutputConfig.Effort)
				} else if effort != "low" {
					if model != "claude-haiku-4-5-20251001" {
						require.Equal(t, "adaptive", out.Thinking.Type)
						require.Zero(t, out.Thinking.BudgetTokens)
					} else {
						require.Equal(t, "enabled", out.Thinking.Type)
						require.GreaterOrEqual(t, out.Thinking.BudgetTokens, 1024)
						require.Less(t, out.Thinking.BudgetTokens, cap)
						if effort == "high" && cap == 4096 {
							require.Equal(t, 4095, out.Thinking.BudgetTokens)
						}
						if effort == "high" && cap == 1025 {
							require.Equal(t, 1024, out.Thinking.BudgetTokens)
						}
					}
				}
			}
		}
	}
	for _, effort := range []string{"minimal", "future", "HIGH"} {
		_, err := ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-sonnet-4-6", Input: json.RawMessage(`"hi"`), Reasoning: &ResponsesReasoning{Effort: effort}})
		require.Error(t, err)
	}
	_, err := ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-sonnet-4-6", Input: json.RawMessage(`"hi"`), ToolChoice: json.RawMessage(`"required"`), Reasoning: &ResponsesReasoning{Effort: "high"}})
	require.ErrorContains(t, err, "forced tool_choice")
}
