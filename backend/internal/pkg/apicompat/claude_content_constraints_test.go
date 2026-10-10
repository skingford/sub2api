package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

var contentContractModels = []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5-20251001", "claude-sonnet-5-5", "claude-opus-5-5", "claude-haiku-5-5"}

func TestClaudeDeveloperAndLegacyHistory(t *testing.T) {
	for _, model := range contentContractModels {
		t.Run(model, func(t *testing.T) {
			var req ChatCompletionsRequest
			require.NoError(t, json.Unmarshal([]byte(`{"messages":[
			{"role":"developer","content":"important"},
			{"role":"user","content":"go"},
			{"role":"assistant","function_call":{"name":"lookup","arguments":"{\"i\":1}"}},
			{"role":"function","name":"lookup","content":"first"},
			{"role":"assistant","function_call":{"name":"lookup","arguments":"{\"i\":2}"}},
			{"role":"function","name":"lookup","content":"second"} ]}`), &req))
			req.Model = model
			out, err := ChatCompletionsToAnthropicRequest(&req)
			require.NoError(t, err)
			require.JSONEq(t, `"important"`, string(out.System))
			require.Len(t, out.Messages, 5)
			first := parseContentBlocks(out.Messages[1].Content)[0]
			second := parseContentBlocks(out.Messages[3].Content)[0]
			require.NotEqual(t, first.ID, second.ID)
			require.JSONEq(t, `{"i":1}`, string(first.Input))
			require.JSONEq(t, `{"i":2}`, string(second.Input))
			a, b := parseContentBlocks(out.Messages[2].Content)[0], parseContentBlocks(out.Messages[4].Content)[0]
			require.Equal(t, first.ID, a.ToolUseID)
			require.Equal(t, second.ID, b.ToolUseID)
			require.JSONEq(t, `"first"`, string(a.Content))
			require.JSONEq(t, `"second"`, string(b.Content))
		})
	}
}

func TestClaudeToolArgumentsRejectMalformedOrNonObject(t *testing.T) {
	for _, arguments := range []string{`{"x":`, `null`, `[]`, `"text"`, `42`, `{} {}`} {
		input, err := json.Marshal([]ResponsesInputItem{{Type: "function_call", CallID: "call_1", Name: "lookup", Arguments: arguments}})
		require.NoError(t, err)
		_, err = ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-sonnet-4-6", Input: input})
		require.ErrorContains(t, err, "arguments must be a JSON object", arguments)
	}
}

func TestClaudeMediaKeepsURLsAndToolDocuments(t *testing.T) {
	var req ResponsesRequest
	require.NoError(t, json.Unmarshal([]byte(`{"model":"claude-sonnet-4-6","input":[
	{"role":"user","content":[{"type":"input_image","image_url":"https://example.invalid/image.png?x=1"}]},
	{"type":"function_call","call_id":"call_1","name":"read","arguments":"{}"},
	{"type":"function_call_output","call_id":"call_1","output":[{"type":"input_image","image_url":"http://example.invalid/tool.png"},{"type":"input_file","file_data":"data:application/pdf;base64,JVBERi0="}]}]}`), &req))
	out, err := ResponsesToAnthropicRequest(&req)
	require.NoError(t, err)
	image := parseContentBlocks(out.Messages[0].Content)[0]
	source, err := json.Marshal(image.Source)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"url","url":"https://example.invalid/image.png?x=1"}`, string(source))
	media := parseContentBlocks(parseContentBlocks(out.Messages[2].Content)[0].Content)
	require.Len(t, media, 2)
	require.Equal(t, "http://example.invalid/tool.png", media[0].Source.URL)
	require.Equal(t, "document", media[1].Type)
	require.Equal(t, "application/pdf", media[1].Source.MediaType)
	require.Equal(t, "JVBERi0=", media[1].Source.Data)
	require.Equal(t, image.Source.URL, anthropicImageToDataURI(image.Source))
	for _, invalid := range []string{"file:///local.png", "javascript:alert(1)", "data:image/png;base64,", "https:///missing-host"} {
		require.Nil(t, dataURIToAnthropicImageSource(invalid))
		input, e := json.Marshal([]ResponsesInputItem{{Role: "user", Content: json.RawMessage(`[{"type":"input_image","image_url":` + string(rawJSONString(invalid)) + `}]`)}})
		require.NoError(t, e)
		_, e = ResponsesToAnthropicRequest(&ResponsesRequest{Model: req.Model, Input: input})
		require.ErrorContains(t, e, "unsupported image source")
	}
}

func TestClaudeContentOrderAndOpaqueHistoryAcrossModels(t *testing.T) {
	for _, model := range contentContractModels {
		t.Run(model, func(t *testing.T) {
			blocks := []AnthropicContentBlock{{Type: "text", Text: "before"}, {Type: "thinking", Thinking: "thought", Signature: "opaque-signature"}, {Type: "redacted_thinking", Data: "opaque-data"}, {Type: "tool_use", ID: "toolu_1", Name: "lookup", Input: json.RawMessage(`{"n":9007199254740993}`)}}
			response := AnthropicToResponsesResponse(&AnthropicResponse{Model: model, Content: blocks})
			require.Len(t, response.Output, 4)
			require.Equal(t, "message", response.Output[0].Type)
			require.Equal(t, "reasoning", response.Output[1].Type)
			require.Equal(t, "reasoning", response.Output[2].Type)
			require.Equal(t, "function_call", response.Output[3].Type)
			chat := ResponsesToChatCompletions(response, model).Choices[0].Message
			history, historyErr := decodeAnthropicHistory(chat.AnthropicContent)
			require.NoError(t, historyErr)
			require.Equal(t, blocks, history)
			require.NotContains(t, string(chat.Content), "opaque")
			require.NotContains(t, chat.ReasoningContent, "opaque")
			encoded, err := json.Marshal(chat)
			require.NoError(t, err)
			var echoed ChatMessage
			require.NoError(t, json.Unmarshal(encoded, &echoed))
			assertChatOpaqueRequest(t, model, echoed, blocks)
			_, err = ChatCompletionsToResponses(&ChatCompletionsRequest{Model: "gpt-4.1", Messages: []ChatMessage{echoed}})
			require.ErrorContains(t, err, "requires an Anthropic target")
			for _, mutate := range []func(*ChatMessage){
				func(m *ChatMessage) { m.Content = json.RawMessage(`"edited"`) },
				func(m *ChatMessage) { m.ToolCalls[0].Function.Arguments = `{"n":9007199254740992}` },
				func(m *ChatMessage) {
					blocks, e := decodeAnthropicHistory(m.AnthropicContent)
					require.NoError(t, e)
					blocks[1].Signature = ""
					m.AnthropicContent = encodeAnthropicHistory(blocks)
				},
			} {
				var altered ChatMessage
				require.NoError(t, json.Unmarshal(encoded, &altered))
				mutate(&altered)
				_, err := ChatCompletionsToAnthropicRequest(&ChatCompletionsRequest{Model: model, Messages: []ChatMessage{altered}})
				require.Error(t, err)
			}
		})
	}
}

func assertChatOpaqueRequest(t *testing.T, model string, message ChatMessage, want []AnthropicContentBlock) {
	t.Helper()
	request := &ChatCompletionsRequest{Model: model, Messages: []ChatMessage{message, {Role: "tool", ToolCallID: message.ToolCalls[0].ID, Content: json.RawMessage(`"ok"`)}}}
	out, err := ChatCompletionsToAnthropicRequest(request)
	require.NoError(t, err)
	require.Equal(t, want, parseContentBlocks(out.Messages[0].Content))
}

func TestClaudeChatStreamOpaqueBlocksAreCompleteAndEmittedOnce(t *testing.T) {
	for _, model := range contentContractModels {
		state := NewAnthropicEventToResponsesState()
		chatState := NewResponsesEventToChatState()
		message := ChatMessage{Role: "assistant"}
		calls := make(map[int]ChatToolCall)
		feed := func(event AnthropicStreamEvent) {
			for _, output := range AnthropicEventToResponsesEvents(&event, state) {
				chunks := ResponsesEventToChatChunks(&output, chatState)
				if output.Type == "response.output_item.done" {
					require.Empty(t, ResponsesEventToChatChunks(&output, chatState), "duplicate history must not be emitted")
				}
				for _, chunk := range chunks {
					for _, choice := range chunk.Choices {
						delta := choice.Delta
						if delta.AnthropicContent != nil {
							message.AnthropicContent += *delta.AnthropicContent
						}
						if delta.Content != nil {
							var text string
							if len(message.Content) > 0 {
								require.NoError(t, json.Unmarshal(message.Content, &text))
							}
							message.Content, _ = json.Marshal(text + *delta.Content)
						}
						if delta.ReasoningContent != nil {
							message.ReasoningContent += *delta.ReasoningContent
						}
						for _, part := range delta.ToolCalls {
							require.NotNil(t, part.Index)
							index := *part.Index
							call := calls[index]
							if part.ID != "" {
								call.ID = part.ID
							}
							if part.Type != "" {
								call.Type = part.Type
							}
							call.Function.Name += part.Function.Name
							call.Function.Arguments += part.Function.Arguments
							calls[index] = call
						}
					}
				}
			}
		}
		feed(AnthropicStreamEvent{Type: "message_start", Message: &AnthropicResponse{ID: "msg_test", Model: model}})
		blocks := []AnthropicContentBlock{{Type: "text", Text: "before"}, {Type: "thinking", Thinking: "thought", Signature: "opaque-signature"}, {Type: "redacted_thinking", Data: "opaque-data"}, {Type: "tool_use", ID: "toolu_1", Name: "lookup", Input: json.RawMessage(`{"n":1}`)}}
		for index, block := range blocks {
			initial := block
			var deltas []AnthropicDelta
			switch block.Type {
			case "text":
				initial.Text = ""
				deltas = []AnthropicDelta{{Type: "text_delta", Text: block.Text}}
			case "thinking":
				initial.Thinking = ""
				initial.Signature = ""
				deltas = []AnthropicDelta{{Type: "thinking_delta", Thinking: block.Thinking}, {Type: "signature_delta", Signature: "opaque-"}, {Type: "signature_delta", Signature: "signature"}}
			case "tool_use":
				initial.Input = json.RawMessage(`{}`)
				deltas = []AnthropicDelta{{Type: "input_json_delta", PartialJSON: `{"n":`}, {Type: "input_json_delta", PartialJSON: `1}`}}
			}
			feed(AnthropicStreamEvent{Type: "content_block_start", Index: &index, ContentBlock: &initial})
			for _, delta := range deltas {
				feed(AnthropicStreamEvent{Type: "content_block_delta", Index: &index, Delta: &delta})
			}
			feed(AnthropicStreamEvent{Type: "content_block_stop", Index: &index})
		}
		feed(AnthropicStreamEvent{Type: "message_stop"})
		for i := 0; i < len(calls); i++ {
			message.ToolCalls = append(message.ToolCalls, calls[i])
		}
		history, e := decodeAnthropicHistory(message.AnthropicContent)
		require.NoError(t, e)
		require.Equal(t, blocks, history, model)
		assertChatOpaqueRequest(t, model, message, blocks)
	}
}
