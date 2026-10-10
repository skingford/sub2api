//go:build unit

package service

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClaudeChatHistoryGatewayRoundTrip(t *testing.T) {
	for _, model := range []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5-20251001", "claude-sonnet-5-5", "claude-opus-5-5", "claude-haiku-5-5"} {
		for _, stream := range []bool{false, true} {
			name := model + map[bool]string{false: "/buffered", true: "/stream"}[stream]
			t.Run(name, func(t *testing.T) {
				want := []apicompat.AnthropicContentBlock{{Type: "text", Text: "before lookup"}, {Type: "thinking", Thinking: "thought fake_lookup", Signature: "synthetic-signature-fake_lookup"}, {Type: "redacted_thinking", Data: "synthetic-data-fake_lookup"}, {Type: "tool_use", ID: "toolu_local", Name: "lookup", Input: json.RawMessage(`{"n":9007199254740993}`)}}
				provider := append([]apicompat.AnthropicContentBlock(nil), want...)
				provider[0].Text = "before fake_lookup"
				provider[3].Name = "fake_lookup"
				var sse strings.Builder
				write := func(value any) {
					data, err := json.Marshal(value)
					require.NoError(t, err)
					var typ struct {
						Type string `json:"type"`
					}
					require.NoError(t, json.Unmarshal(data, &typ))
					sse.WriteString("event: " + typ.Type + "\ndata: " + string(data) + "\n\n")
				}
				write(apicompat.AnthropicStreamEvent{Type: "message_start", Message: &apicompat.AnthropicResponse{ID: "msg_local", Type: "message", Role: "assistant", Model: model}})
				for index, block := range provider {
					start := block
					var deltas []apicompat.AnthropicDelta
					switch block.Type {
					case "text":
						start.Text = ""
						// Alias split across chunks exercises the exact visible
						// projection, not an assumed whole-text replacement.
						deltas = []apicompat.AnthropicDelta{{Type: "text_delta", Text: "before fake_"}, {Type: "text_delta", Text: "lookup"}}
					case "thinking":
						start.Thinking = ""
						start.Signature = ""
						deltas = []apicompat.AnthropicDelta{{Type: "thinking_delta", Thinking: block.Thinking}, {Type: "signature_delta", Signature: block.Signature[:10]}, {Type: "signature_delta", Signature: block.Signature[10:]}}
					case "tool_use":
						start.Input = json.RawMessage(`{}`)
						deltas = []apicompat.AnthropicDelta{{Type: "input_json_delta", PartialJSON: string(block.Input)}}
					}
					write(apicompat.AnthropicStreamEvent{Type: "content_block_start", Index: &index, ContentBlock: &start})
					for _, delta := range deltas {
						write(apicompat.AnthropicStreamEvent{Type: "content_block_delta", Index: &index, Delta: &delta})
					}
					write(apicompat.AnthropicStreamEvent{Type: "content_block_stop", Index: &index})
				}
				write(apicompat.AnthropicStreamEvent{Type: "message_delta", Delta: &apicompat.AnthropicDelta{StopReason: "tool_use"}})
				write(apicompat.AnthropicStreamEvent{Type: "message_stop"})
				svc, _ := newClaudeContractGateway(t)
				rec := httptest.NewRecorder()
				ctx, _ := gin.CreateTestContext(rec)
				ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
				ctx.Set(toolNameRewriteKey, &ToolNameRewrite{ReverseOrdered: [][2]string{{"fake_lookup", "lookup"}}})
				response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(sse.String()))}
				var message apicompat.ChatMessage
				if !stream {
					_, err := svc.handleCCBufferedFromAnthropic(response, ctx, "local-model-alias", model, nil, time.Now())
					require.NoError(t, err)
					var result apicompat.ChatCompletionsResponse
					require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
					message = result.Choices[0].Message
				} else {
					_, err := svc.handleCCStreamingFromAnthropic(response, ctx, "local-model-alias", model, nil, time.Now())
					require.NoError(t, err)
					var text strings.Builder
					calls := map[int]apicompat.ChatToolCall{}
					historyChunks, finished := 0, false
					for _, line := range strings.Split(rec.Body.String(), "\n") {
						if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
							continue
						}
						var chunk apicompat.ChatCompletionsChunk
						require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk))
						for _, choice := range chunk.Choices {
							delta := choice.Delta
							if delta.AnthropicContent != nil {
								require.False(t, finished)
								historyChunks++
								message.AnthropicContent += *delta.AnthropicContent
							}
							if choice.FinishReason != nil {
								finished = true
							}
							if delta.Content != nil {
								text.WriteString(*delta.Content)
							}
							if delta.ReasoningContent != nil {
								message.ReasoningContent += *delta.ReasoningContent
							}
							for _, part := range delta.ToolCalls {
								require.NotNil(t, part.Index)
								c := calls[*part.Index]
								if part.ID != "" {
									c.ID = part.ID
								}
								c.Function.Name += part.Function.Name
								c.Function.Arguments += part.Function.Arguments
								c.Type = "function"
								calls[*part.Index] = c
							}
						}
					}
					require.Equal(t, 1, historyChunks)
					require.True(t, finished)
					message.Role = "assistant"
					message.Content, _ = json.Marshal(text.String())
					for i := 0; i < len(calls); i++ {
						message.ToolCalls = append(message.ToolCalls, calls[i])
					}
				}
				require.True(t, strings.HasPrefix(message.AnthropicContent, "anthropic-history-v1:"))
				decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(message.AnthropicContent, "anthropic-history-v1:"))
				require.NoError(t, err)
				var history struct {
					Content []apicompat.AnthropicContentBlock `json:"content"`
				}
				require.NoError(t, json.Unmarshal(decoded, &history))
				require.Equal(t, want, history.Content)
				var actual []apicompat.AnthropicContentBlock
				echo := apicompat.ChatCompletionsRequest{Model: model, Messages: []apicompat.ChatMessage{message, {Role: "tool", ToolCallID: message.ToolCalls[0].ID, Content: json.RawMessage(`"ok"`)}}}
				out, err := apicompat.ChatCompletionsToAnthropicRequest(&echo)
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(out.Messages[0].Content, &actual))
				require.Equal(t, want, actual)
			})
		}
	}
}

func TestClaudeChatInvalidHistoryRejectedBeforeDispatch(t *testing.T) {
	svc, up := newClaudeContractGateway(t)
	_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeAPIKey), []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"assistant","content":"text","anthropic_content":"not-a-valid-history"}]}`), nil, "/v1/chat/completions")
	require.Error(t, err)
	require.Equal(t, 400, rec.Code)
	require.Zero(t, up.calls)
}
