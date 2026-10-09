package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHaiku55ResponsesAndChatControls(t *testing.T) {
	for _, effort := range []string{"", "low", "medium", "high", "xhigh", "max"} {
		for _, chat := range []bool{false, true} {
			req := &ResponsesRequest{Model: "claude-haiku-5-5", Input: json.RawMessage(`"hello"`), Reasoning: &ResponsesReasoning{Effort: effort}}
			if chat {
				var err error
				req, err = ChatCompletionsToResponses(&ChatCompletionsRequest{Model: req.Model, ReasoningEffort: effort, Messages: []ChatMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}}})
				require.NoError(t, err)
			}
			out, err := ResponsesToAnthropicRequest(req)
			require.NoError(t, err)
			require.Equal(t, 128000, out.MaxTokens)
			require.Equal(t, &AnthropicThinking{Type: "adaptive", Display: "omitted"}, out.Thinking)
			want := effort
			if want == "" {
				want = "medium"
			}
			require.Equal(t, want, out.OutputConfig.Effort)
		}
	}
	maxTokens, temperature := 4096, 0.4
	out, err := ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-haiku-5-5", Input: json.RawMessage(`"hello"`), MaxOutputTokens: &maxTokens, Temperature: &temperature})
	require.NoError(t, err)
	require.Equal(t, maxTokens, out.MaxTokens)
	require.Equal(t, &temperature, out.Temperature)
	_, err = ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-haiku-5-5", Input: json.RawMessage(`"hello"`), Reasoning: &ResponsesReasoning{Effort: "none"}})
	require.ErrorContains(t, err, "reasoning effort")
}

func TestHaiku55SignedThinkingToolRoundTrip(t *testing.T) {
	for _, block := range []AnthropicContentBlock{{Type: "thinking", Signature: "synthetic-opaque-signature"}, {Type: "redacted_thinking", Data: "synthetic-opaque-data"}} {
		response := AnthropicToResponsesResponse(&AnthropicResponse{Model: "claude-haiku-5-5", Content: []AnthropicContentBlock{block, {Type: "text", Text: "progress"}, {Type: "tool_use", ID: "toolu_local", Name: "lookup", Input: json.RawMessage(`{}`)}}})
		require.Len(t, response.Output, 3)
		require.NotEmpty(t, response.Output[0].EncryptedContent)
		raw, err := json.Marshal(response.Output)
		require.NoError(t, err)
		var items []ResponsesInputItem
		require.NoError(t, json.Unmarshal(raw, &items))
		items = append(items, ResponsesInputItem{Type: "function_call_output", CallID: response.Output[2].CallID, Output: "ok"})
		raw, err = json.Marshal(items)
		require.NoError(t, err)
		converted, err := ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-haiku-5-5", Input: raw})
		require.NoError(t, err)
		var blocks []AnthropicContentBlock
		require.NoError(t, json.Unmarshal(converted.Messages[0].Content, &blocks))
		require.Equal(t, block, blocks[0])
		require.Equal(t, "text", blocks[1].Type)
		require.Equal(t, "tool_use", blocks[2].Type)
	}
	state := NewAnthropicEventToResponsesState()
	anthToResHandleMessageStart(&AnthropicStreamEvent{Message: &AnthropicResponse{Model: "claude-haiku-5-5"}}, state)
	require.True(t, state.PreserveThinkingSignatures)
}
