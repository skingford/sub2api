//go:build unit

// Replays the same captured synthetic SSE through actual Chat handlers and
// then through the production request path, for both account types.
package service

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClaudeChatGatewayHistoryAudit(t *testing.T) {
	input, output := os.Getenv("CLAUDE_RESPONSE_FIXTURES"), os.Getenv("CLAUDE_CHAT_HISTORY_OUTPUT")
	if input == "" || output == "" {
		t.Skip("captured fixtures and output directory required")
	}
	data, err := os.ReadFile(input)
	require.NoError(t, err)
	var fixtures []struct {
		Name     string                      `json:"name"`
		Response apicompat.AnthropicResponse `json:"response"`
		SSE      string                      `json:"sse"`
	}
	require.NoError(t, json.Unmarshal(data, &fixtures))
	require.NoError(t, os.MkdirAll(filepath.Join(output, "exports"), 0700))
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
		for _, stream := range []bool{false, true} {
			svc, _ := newClaudeContractGateway(t)
			rec := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(rec)
			ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
			response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(fixture.SSE))}
			var message apicompat.ChatMessage
			if !stream {
				_, err = svc.handleCCBufferedFromAnthropic(response, ctx, fixture.Response.Model, fixture.Response.Model, nil, time.Now())
				require.NoError(t, err)
				var result apicompat.ChatCompletionsResponse
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
				message = result.Choices[0].Message
			} else {
				_, err = svc.handleCCStreamingFromAnthropic(response, ctx, fixture.Response.Model, fixture.Response.Model, nil, time.Now())
				require.NoError(t, err)
				var text strings.Builder
				calls := map[int]apicompat.ChatToolCall{}
				for _, line := range strings.Split(rec.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
						continue
					}
					var chunk apicompat.ChatCompletionsChunk
					require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk))
					for _, choice := range chunk.Choices {
						delta := choice.Delta
						if delta.AnthropicContent != nil {
							message.AnthropicContent += *delta.AnthropicContent
						}
						if delta.Content != nil {
							text.WriteString(*delta.Content)
						}
						if delta.ReasoningContent != nil {
							message.ReasoningContent += *delta.ReasoningContent
						}
						for _, part := range delta.ToolCalls {
							require.NotNil(t, part.Index)
							call := calls[*part.Index]
							if part.ID != "" {
								call.ID = part.ID
							}
							call.Type = "function"
							call.Function.Name += part.Function.Name
							call.Function.Arguments += part.Function.Arguments
							calls[*part.Index] = call
						}
					}
				}
				message.Role = "assistant"
				message.Content, _ = json.Marshal(text.String())
				for i := 0; i < len(calls); i++ {
					message.ToolCalls = append(message.ToolCalls, calls[i])
				}
			}
			require.NotEmpty(t, message.AnthropicContent)
			messages := []apicompat.ChatMessage{{Role: "user", Content: json.RawMessage(`"LOCAL_HISTORY_CONTINUATION"`)}, message}
			for _, call := range message.ToolCalls {
				messages = append(messages, apicompat.ChatMessage{Role: "tool", ToolCallID: call.ID, Content: json.RawMessage(`"LOCAL_TOOL_RESULT"`)})
			}
			if len(message.ToolCalls) == 0 {
				messages = append(messages, apicompat.ChatMessage{Role: "user", Content: json.RawMessage(`"continue"`)})
			}
			request, err := json.Marshal(apicompat.ChatCompletionsRequest{Model: fixture.Response.Model, Messages: messages})
			require.NoError(t, err)
			for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
				svc, up := newClaudeContractGateway(t)
				_, rec, callErr := callClaudeContract(t, svc, newClaude2292Account(kind), request, nil, "/v1/chat/completions")
				name := strings.ReplaceAll(fixture.Name, "/", "_") + "-" + kind + map[bool]string{false: "-buffered", true: "-stream"}[stream]
				row := map[string]any{"name": name, "model": fixture.Response.Model, "account": kind, "stream": stream, "source_opaque": opaque, "chat_message": message, "status": rec.Code, "calls": up.calls}
				if callErr != nil {
					row["error"] = callErr.Error()
				}
				if up.request != nil {
					row["next_anthropic_request"] = json.RawMessage(up.body)
					headers := map[string]string{}
					for key, values := range up.request.Header {
						require.Len(t, values, 1)
						headers[key] = values[0]
					}
					export := map[string]any{"method": up.request.Method, "path": up.request.URL.RequestURI(), "headers": headers, "raw_body_utf8": string(up.body), "wire_body_base64": base64.StdEncoding.EncodeToString(up.body), "upstream_profile": HTTPUpstreamProfileFromContext(up.request.Context()), "synthetic_auth": kind, "audit_case": name}
					b, err := json.MarshalIndent(export, "", "  ")
					require.NoError(t, err)
					require.NoError(t, os.WriteFile(filepath.Join(output, "exports", name+".json"), append(b, '\n'), 0600))
				}
				rows = append(rows, row)
			}
		}
	}
	data, err = json.MarshalIndent(rows, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(output, "observations.json"), append(data, '\n'), 0600))
}
