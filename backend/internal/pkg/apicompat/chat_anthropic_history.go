package apicompat

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// anthropic_content is a bridge extension, not an OpenAI ciphertext format.
// It carries complete assistant blocks because flattening text/tool fields
// alone cannot reconstruct the positions of signed or redacted thinking.
func responsesOutputAnthropicBlocks(item ResponsesOutput) []AnthropicContentBlock {
	switch item.Type {
	case "message":
		var blocks []AnthropicContentBlock
		for _, part := range item.Content {
			if part.Type == "output_text" {
				blocks = append(blocks, AnthropicContentBlock{Type: "text", Text: part.Text})
			}
		}
		return blocks
	case "function_call":
		input := json.RawMessage(item.Arguments)
		if item.Arguments == "" {
			input = json.RawMessage(`{}`)
		}
		if object, ok := rawJSONObject(input); ok && object != nil {
			return []AnthropicContentBlock{{Type: "tool_use", ID: fromResponsesCallIDToAnthropic(item.CallID), Name: item.Name, Input: input}}
		}
	case "reasoning":
		if strings.HasPrefix(item.EncryptedContent, anthropicThinkingEnvelopePrefix) {
			block, err := decodeAnthropicThinking(item.EncryptedContent)
			if err == nil {
				return []AnthropicContentBlock{block}
			}
		}
	}
	return nil
}

func hasOpaqueAnthropicThinking(blocks []AnthropicContentBlock) bool {
	for _, block := range blocks {
		if (block.Type == "thinking" && block.Signature != "") || (block.Type == "redacted_thinking" && block.Data != "") {
			return true
		}
	}
	return false
}

func decodeAnthropicThinking(envelope string) (AnthropicContentBlock, error) {
	var block AnthropicContentBlock
	if !strings.HasPrefix(envelope, anthropicThinkingEnvelopePrefix) {
		return block, fmt.Errorf("not an Anthropic thinking envelope")
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(envelope, anthropicThinkingEnvelopePrefix))
	if err != nil {
		return block, fmt.Errorf("invalid Anthropic thinking envelope: %w", err)
	}
	if err := json.Unmarshal(raw, &block); err != nil {
		return block, fmt.Errorf("invalid Anthropic thinking block: %w", err)
	}
	if !hasOpaqueAnthropicThinking([]AnthropicContentBlock{block}) {
		return block, fmt.Errorf("invalid Anthropic signed thinking block")
	}
	return block, nil
}

const anthropicHistoryPrefix = "anthropic-history-v1:"

type chatAnthropicHistory struct {
	Content   []AnthropicContentBlock `json:"content"`
	Text      string                  `json:"text"`
	ToolCalls []ChatToolCall          `json:"tool_calls,omitempty"`
}

func encodeAnthropicHistory(blocks []AnthropicContentBlock) string {
	history := chatAnthropicHistory{Content: blocks}
	for _, block := range blocks {
		switch block.Type {
		case "text":
			history.Text += block.Text
		case "tool_use":
			history.ToolCalls = append(history.ToolCalls, ChatToolCall{ID: toResponsesCallID(block.ID), Type: "function", Function: ChatFunctionCall{Name: block.Name, Arguments: string(block.Input)}})
		}
	}
	return encodeChatAnthropicHistory(history)
}

func encodeChatAnthropicHistory(history chatAnthropicHistory) string {
	payload, err := json.Marshal(history)
	if err != nil {
		return ""
	}
	return anthropicHistoryPrefix + base64.RawStdEncoding.EncodeToString(payload)
}

func decodeChatAnthropicHistory(history string) (chatAnthropicHistory, error) {
	var value chatAnthropicHistory
	if !strings.HasPrefix(history, anthropicHistoryPrefix) {
		return value, fmt.Errorf("invalid anthropic_content history prefix")
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(history, anthropicHistoryPrefix))
	if err != nil {
		return value, fmt.Errorf("invalid anthropic_content encoding: %w", err)
	}
	if err = json.Unmarshal(raw, &value); err != nil {
		return value, fmt.Errorf("invalid anthropic_content blocks: %w", err)
	}
	if !hasOpaqueAnthropicThinking(value.Content) {
		return value, fmt.Errorf("anthropic_content requires signed or redacted thinking")
	}
	return value, nil
}

func decodeAnthropicHistory(history string) ([]AnthropicContentBlock, error) {
	value, err := decodeChatAnthropicHistory(history)
	return value.Content, err
}

// RewriteChatAnthropicHistory restores request-local tool aliases in ordinary
// content without touching signed thinking bytes. The projection records what
// the client actually received, including streaming chunk boundary behavior.
func RewriteChatAnthropicHistory(history string, projection ChatMessage, rewrite func([]byte) []byte) (string, error) {
	value, err := decodeChatAnthropicHistory(history)
	if err != nil {
		return "", err
	}
	for i, block := range value.Content {
		if block.Type == "thinking" || block.Type == "redacted_thinking" {
			continue
		}
		raw, err := json.Marshal(block)
		if err != nil {
			return "", err
		}
		if err = json.Unmarshal(rewrite(raw), &value.Content[i]); err != nil {
			return "", err
		}
	}
	value.Text, err = parseAssistantContent(projection.Content)
	if err != nil {
		return "", err
	}
	value.ToolCalls = projection.ToolCalls
	return encodeChatAnthropicHistory(value), nil
}

func chatAnthropicHistoryToResponses(message ChatMessage) ([]ResponsesInputItem, error) {
	history, err := decodeChatAnthropicHistory(message.AnthropicContent)
	if err != nil {
		return nil, err
	}
	var items []ResponsesInputItem
	var calls []AnthropicContentBlock
	for _, block := range history.Content {
		switch block.Type {
		case "text":
			content, err := json.Marshal([]ResponsesContentPart{{Type: "output_text", Text: block.Text}})
			if err != nil {
				return nil, err
			}
			items = append(items, ResponsesInputItem{Type: "message", Role: "assistant", Content: content})
		case "thinking", "redacted_thinking":
			if !hasOpaqueAnthropicThinking([]AnthropicContentBlock{block}) {
				return nil, fmt.Errorf("anthropic_content contains unsigned thinking")
			}
			items = append(items, ResponsesInputItem{Type: "reasoning", EncryptedContent: encodeAnthropicThinking(block)})
		case "tool_use":
			if object, ok := rawJSONObject(block.Input); !ok || object == nil || block.ID == "" || block.Name == "" {
				return nil, fmt.Errorf("anthropic_content contains an invalid tool call")
			}
			calls = append(calls, block)
			items = append(items, ResponsesInputItem{Type: "function_call", CallID: toResponsesCallID(block.ID), Name: block.Name, Arguments: string(block.Input)})
		default:
			return nil, fmt.Errorf("unsupported anthropic_content block %q", block.Type)
		}
	}
	// Reject stale extensions after a client edits visible history. Never
	// silently replace the client's text or tools with an older signed turn.
	visible, err := parseAssistantContent(message.Content)
	if err != nil {
		return nil, err
	}
	if visible != history.Text || len(message.ToolCalls) != len(calls) || len(history.ToolCalls) != len(calls) || message.FunctionCall != nil {
		return nil, fmt.Errorf("anthropic_content does not match assistant content or tool_calls")
	}
	for i, call := range history.ToolCalls {
		visible := message.ToolCalls[i]
		if visible.ID != call.ID || visible.Function.Name != call.Function.Name || !sameToolJSON(visible.Function.Arguments, json.RawMessage(call.Function.Arguments)) {
			return nil, fmt.Errorf("anthropic_content does not match tool_calls")
		}
	}
	return items, nil
}

func sameToolJSON(arguments string, expected json.RawMessage) bool {
	if arguments == "" {
		arguments = "{}"
	}
	if !json.Valid([]byte(arguments)) {
		return false
	}
	decode := func(raw []byte) any {
		var value any
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil {
			return nil
		}
		return value
	}
	return reflect.DeepEqual(decode([]byte(arguments)), decode(expected))
}

// Completed blocks are accumulated once, then encoded in one string delta.
// This avoids requiring generic Chat stream accumulators to merge block arrays.
func resToChatHandleAnthropicItem(index int, item *ResponsesOutput, state *ResponsesEventToChatState) []ChatCompletionsChunk {
	if item == nil || state.AnthropicOutputDone[index] {
		return nil
	}
	if state.AnthropicOutputDone == nil {
		state.AnthropicOutputDone = make(map[int]bool)
	}
	state.AnthropicOutputDone[index] = true
	state.AnthropicContent = append(state.AnthropicContent, responsesOutputAnthropicBlocks(*item)...)
	return nil
}

func emitAnthropicChatHistory(state *ResponsesEventToChatState) []ChatCompletionsChunk {
	if state.AnthropicHistoryStarted || !hasOpaqueAnthropicThinking(state.AnthropicContent) {
		return nil
	}
	state.AnthropicHistoryStarted = true
	history := encodeAnthropicHistory(state.AnthropicContent)
	return []ChatCompletionsChunk{makeChatDeltaChunk(state, ChatDelta{AnthropicContent: &history})}
}
