package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChatToolStrictOmissionIsDestinationSpecific(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, value := range []string{"omitted", "false", "true"} {
			t.Run(value+map[bool]string{false: "/tools", true: "/functions"}[legacy], func(t *testing.T) {
				function := ChatFunction{Name: "lookup", Parameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}
				if value != "omitted" {
					strict := value == "true"
					function.Strict = &strict
				}
				req := &ChatCompletionsRequest{Model: "claude-sonnet-4-6", Messages: []ChatMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}}}
				if legacy {
					req.Functions = []ChatFunction{function}
				} else {
					req.Tools = []ChatTool{{Type: "function", Function: &function}}
				}
				anthropic, err := ChatCompletionsToAnthropicRequest(req)
				require.NoError(t, err)
				encoded, err := json.Marshal(anthropic)
				require.NoError(t, err)
				var output struct {
					Tools []map[string]any `json:"tools"`
				}
				require.NoError(t, json.Unmarshal(encoded, &output))
				require.Len(t, output.Tools, 1)
				actual, exists := output.Tools[0]["strict"]
				require.Equal(t, value != "omitted", exists)
				if exists {
					require.Equal(t, value == "true", actual)
				}
				// The real OpenAI Responses route still needs its original default.
				openAI, err := ChatCompletionsToResponses(req)
				require.NoError(t, err)
				require.Len(t, openAI.Tools, 1)
				require.NotNil(t, openAI.Tools[0].Strict)
				require.Equal(t, value == "true", *openAI.Tools[0].Strict)
			})
		}
	}
}
