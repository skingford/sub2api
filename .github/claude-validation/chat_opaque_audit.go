// Build through an overlay at backend/internal/repository/testdata/claude_capture_probe.go.
// This offline component probe uses captured synthetic Anthropic responses.
package main

import (
	"encoding/json"
	"os"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

func main() {
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	var fixtures []struct {
		Name     string                      `json:"name"`
		Response apicompat.AnthropicResponse `json:"response"`
	}
	if err = json.Unmarshal(raw, &fixtures); err != nil {
		panic(err)
	}
	var rows []map[string]any
	for _, fixture := range fixtures {
		var opaque []apicompat.AnthropicContentBlock
		for _, block := range fixture.Response.Content {
			if block.Type == "thinking" || block.Type == "redacted_thinking" {
				opaque = append(opaque, block)
			}
		}
		if len(opaque) == 0 {
			continue
		}
		chat := apicompat.ResponsesToChatCompletions(apicompat.AnthropicToResponsesResponse(&fixture.Response), fixture.Response.Model)
		messages := []apicompat.ChatMessage{{Role: "user", Content: json.RawMessage(`"LOCAL_OPAQUE_ROUND_TRIP"`)}, chat.Choices[0].Message}
		for _, call := range chat.Choices[0].Message.ToolCalls {
			messages = append(messages, apicompat.ChatMessage{Role: "tool", ToolCallID: call.ID, Content: json.RawMessage(`"LOCAL_TOOL_RESULT"`)})
		}
		if len(chat.Choices[0].Message.ToolCalls) == 0 {
			messages = append(messages, apicompat.ChatMessage{Role: "user", Content: json.RawMessage(`"continue"`)})
		}
		converted, convertErr := apicompat.ChatCompletionsToAnthropicRequest(&apicompat.ChatCompletionsRequest{Model: fixture.Response.Model, Messages: messages})
		row := map[string]any{"name": fixture.Name, "model": fixture.Response.Model, "source_opaque": opaque, "chat": chat, "next_anthropic_request": converted}
		if convertErr != nil {
			row["error"] = convertErr.Error()
		}
		rows = append(rows, row)
	}
	out, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		panic(err)
	}
	if err = os.WriteFile(os.Args[2], append(out, '\n'), 0600); err != nil {
		panic(err)
	}
}
