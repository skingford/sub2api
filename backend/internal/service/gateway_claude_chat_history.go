package service

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
)

// Keep the encoded history in sync with visible tool alias restoration, while
// preserving the provider's signed thinking/data bytes exactly.
func restoreClaudeChatResponse(c *gin.Context, payload []byte) ([]byte, error) {
	restored := reverseToolNamesIfPresent(c, payload)
	if !bytes.Contains(payload, []byte(`"anthropic_content"`)) {
		return restored, nil
	}
	var response apicompat.ChatCompletionsResponse
	if err := json.Unmarshal(restored, &response); err != nil {
		return nil, err
	}
	for i := range response.Choices {
		message := &response.Choices[i].Message
		if message.AnthropicContent == "" {
			continue
		}
		history, err := apicompat.RewriteChatAnthropicHistory(message.AnthropicContent, *message, func(raw []byte) []byte { return reverseToolNamesIfPresent(c, raw) })
		if err != nil {
			return nil, err
		}
		message.AnthropicContent = history
	}
	return json.Marshal(response)
}

type claudeChatWireProjection struct {
	text  strings.Builder
	calls map[int]apicompat.ChatToolCall
}

func (p *claudeChatWireProjection) restore(c *gin.Context, chunk apicompat.ChatCompletionsChunk) (string, error) {
	data, err := json.Marshal(chunk)
	if err != nil {
		return "", err
	}
	var visible apicompat.ChatCompletionsChunk
	if err = json.Unmarshal(reverseToolNamesIfPresent(c, data), &visible); err != nil {
		return "", err
	}
	for i := range visible.Choices {
		delta := &visible.Choices[i].Delta
		if delta.Content != nil {
			_, _ = p.text.WriteString(*delta.Content) // strings.Builder.WriteString always returns a nil error.
		}
		for _, part := range delta.ToolCalls {
			if part.Index == nil {
				continue
			}
			if p.calls == nil {
				p.calls = make(map[int]apicompat.ChatToolCall)
			}
			call := p.calls[*part.Index]
			if part.ID != "" {
				call.ID = part.ID
			}
			if part.Type != "" {
				call.Type = part.Type
			}
			call.Function.Name += part.Function.Name
			call.Function.Arguments += part.Function.Arguments
			p.calls[*part.Index] = call
		}
		if delta.AnthropicContent == nil {
			continue
		}
		projection := apicompat.ChatMessage{Role: "assistant"}
		projection.Content, err = json.Marshal(p.text.String())
		if err != nil {
			return "", err
		}
		for index := 0; index < len(p.calls); index++ {
			projection.ToolCalls = append(projection.ToolCalls, p.calls[index])
		}
		history, err := apicompat.RewriteChatAnthropicHistory(*chunk.Choices[i].Delta.AnthropicContent, projection, func(raw []byte) []byte { return reverseToolNamesIfPresent(c, raw) })
		if err != nil {
			return "", err
		}
		delta.AnthropicContent = &history
	}
	return apicompat.ChatChunkToSSE(visible)
}
