package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

const constraintSchema = `{"type":"object","properties":{"ok":{"type":"boolean"},"note":{"type":"string","enum":["是","no"]}},"required":["ok","note"],"additionalProperties":false}`

func TestAnthropicSchemaSurvivesReasoningBranches(t *testing.T) {
	for _, model := range []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5-20251001", "claude-sonnet-5-5", "claude-opus-5-5", "claude-haiku-5-5"} {
		for _, effort := range []string{"", "low", "high"} {
			t.Run(model+"/"+effort, func(t *testing.T) {
				out, err := ResponsesToAnthropicRequest(&ResponsesRequest{Model: model, Input: json.RawMessage(`"hello"`), Text: &ResponsesText{Format: json.RawMessage(`{"type":"json_schema","name":"answer","strict":true,"schema":` + constraintSchema + `}`)}, Reasoning: &ResponsesReasoning{Effort: effort}})
				require.NoError(t, err)
				require.NotNil(t, out.OutputConfig)
				var format struct {
					Type   string
					Schema json.RawMessage
				}
				require.NoError(t, json.Unmarshal(out.OutputConfig.Format, &format))
				require.Equal(t, "json_schema", format.Type)
				require.JSONEq(t, constraintSchema, string(format.Schema))
				if effort != "" {
					require.Equal(t, effort, out.OutputConfig.Effort)
				}
			})
		}
	}
	out, err := ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-sonnet-5-5", Input: json.RawMessage(`"hello"`), Text: &ResponsesText{Format: json.RawMessage(`{"type":"json_schema","schema":` + constraintSchema + `}`)}, Reasoning: &ResponsesReasoning{Effort: "none"}})
	require.NoError(t, err)
	require.Equal(t, "between_tools", out.Thinking.Type)
	require.NotEmpty(t, out.OutputConfig.Format)
}

func TestAnthropicChatConstraintsDoNotUseResponsesFloor(t *testing.T) {
	for _, n := range []int{1, 64, 127, 128, 4096} {
		req := &ChatCompletionsRequest{Model: "claude-haiku-5-5", MaxTokens: &n, Stop: json.RawMessage(`["STOP","停\n止"]`), Messages: []ChatMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}}, ResponseFormat: json.RawMessage(`{"type":"json_schema","json_schema":{"name":"answer","strict":true,"schema":` + constraintSchema + `}}`)}
		out, err := ChatCompletionsToAnthropicRequest(req)
		require.NoError(t, err)
		require.Equal(t, n, out.MaxTokens)
		require.Equal(t, []string{"STOP", "停\n止"}, out.StopSeqs)
		require.NotEmpty(t, out.OutputConfig.Format)
		openAI, err := ChatCompletionsToResponses(req)
		require.NoError(t, err)
		require.Equal(t, max(n, minMaxOutputTokens), *openAI.MaxOutputTokens, "real Responses requests keep their own contract")
	}
	maxTokens, completion := 64, 7
	out, err := ChatCompletionsToAnthropicRequest(&ChatCompletionsRequest{Model: "claude-haiku-5-5", MaxTokens: &maxTokens, MaxCompletionTokens: &completion, Messages: []ChatMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}}})
	require.NoError(t, err)
	require.Equal(t, 7, out.MaxTokens)
}

func TestAnthropicConstraintValidation(t *testing.T) {
	for _, raw := range []string{`{"type":"json_object"}`, `{"type":"unknown"}`, `{"type":"json_schema"}`, `{"type":"json_schema","schema":null}`, `{"type":"json_schema","schema":[]}`, `[]`} {
		_, err := ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-sonnet-4-6", Input: json.RawMessage(`"hi"`), Text: &ResponsesText{Format: json.RawMessage(raw)}})
		require.Error(t, err, raw)
	}
	for _, raw := range []string{`null`, `{"type":"text"}`} {
		out, err := ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-sonnet-4-6", Input: json.RawMessage(`"hi"`), Text: &ResponsesText{Format: json.RawMessage(raw)}})
		require.NoError(t, err)
		require.Nil(t, out.OutputConfig)
	}
	for _, raw := range []string{`1`, `{}`, `[null]`, `["ok",3]`} {
		_, err := chatStopSequences(json.RawMessage(raw))
		require.Error(t, err, raw)
	}
	for _, raw := range []string{`null`, `[]`, `""`, `"STOP"`, `["A","B"]`} {
		_, err := chatStopSequences(json.RawMessage(raw))
		require.NoError(t, err, raw)
	}
	for _, n := range []int{0, -1} {
		_, err := ChatCompletionsToAnthropicRequest(&ChatCompletionsRequest{Model: "claude-haiku-5-5", MaxTokens: &n, Messages: []ChatMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}}})
		require.ErrorContains(t, err, "positive")
	}
}
