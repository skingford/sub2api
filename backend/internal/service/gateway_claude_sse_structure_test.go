//go:build unit

package service

import (
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
	"github.com/tidwall/gjson"
)

func TestClaudeSSEStructureContract(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name          string  `json:"name"`
			Success       bool    `json:"success"`
			Text          string  `json:"text"`
			ToolArguments *string `json:"tool_arguments"`
			InputTokens   int     `json:"input_tokens"`
			OutputTokens  int     `json:"output_tokens"`
			ResponseCount int     `json:"response_count"`
			Body          string  `json:"body"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile("testdata/claude_sse_structure_contract.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &fixture))
	require.Len(t, fixture.Cases, 32)
	for _, tc := range fixture.Cases {
		wire := tc.Body
		if root := os.Getenv("CLAUDE_SSE_STRUCTURE_CAPTURE_ROOT"); root != "" {
			raw, err := os.ReadFile(filepath.Join(root, tc.Name, "responses.json"))
			require.NoError(t, err)
			var responses []struct {
				Body string `json:"body"`
			}
			require.NoError(t, json.Unmarshal(raw, &responses))
			require.Len(t, responses, tc.ResponseCount)
			wire = responses[0].Body
		}
		for _, target := range []string{"responses-buffered", "responses-stream", "chat-buffered", "chat-stream"} {
			t.Run(tc.Name+"/"+target, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				svc := &GatewayService{}
				resp := &http.Response{Header: make(http.Header), Body: io.NopCloser(strings.NewReader(wire))}
				var result *ForwardResult
				var err error
				require.NotPanics(t, func() {
					switch target {
					case "responses-buffered":
						result, err = svc.handleResponsesBufferedStreamingResponse(resp, c, "claude-sonnet-4-6", "claude-sonnet-4-6", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
					case "responses-stream":
						result, err = svc.handleResponsesStreamingResponse(resp, c, "claude-sonnet-4-6", "claude-sonnet-4-6", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
					case "chat-buffered":
						result, err = svc.handleCCBufferedFromAnthropic(resp, c, "claude-sonnet-4-6", "claude-sonnet-4-6", nil, time.Now())
					case "chat-stream":
						result, err = svc.handleCCStreamingFromAnthropic(resp, c, "claude-sonnet-4-6", "claude-sonnet-4-6", nil, time.Now())
					}
				})
				if !tc.Success {
					require.Error(t, err)
					require.True(t, IsResponseCommitted(c))
					require.Contains(t, rec.Body.String(), `"error"`)
					require.NotContains(t, rec.Body.String(), "response.completed")
					require.NotContains(t, rec.Body.String(), "[DONE]")
					if strings.HasSuffix(target, "buffered") {
						require.Equal(t, http.StatusBadGateway, rec.Code)
					}
					return
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, tc.InputTokens, result.Usage.InputTokens)
				require.Equal(t, tc.OutputTokens, result.Usage.OutputTokens)
				text, arguments, toolName := "", "", ""
				switch target {
				case "responses-buffered":
					text = gjson.GetBytes(rec.Body.Bytes(), "output.0.content.0.text").String()
					arguments = gjson.GetBytes(rec.Body.Bytes(), "output.0.arguments").String()
					toolName = gjson.GetBytes(rec.Body.Bytes(), "output.0.name").String()
				case "chat-buffered":
					text = gjson.GetBytes(rec.Body.Bytes(), "choices.0.message.content").String()
					arguments = gjson.GetBytes(rec.Body.Bytes(), "choices.0.message.tool_calls.0.function.arguments").String()
					toolName = gjson.GetBytes(rec.Body.Bytes(), "choices.0.message.tool_calls.0.function.name").String()
				default:
					for _, line := range strings.Split(rec.Body.String(), "\n") {
						if !strings.HasPrefix(line, "data: ") {
							continue
						}
						payload := strings.TrimPrefix(line, "data: ")
						if target == "chat-stream" {
							text += gjson.Get(payload, "choices.0.delta.content").String()
							arguments += gjson.Get(payload, "choices.0.delta.tool_calls.0.function.arguments").String()
							toolName += gjson.Get(payload, "choices.0.delta.tool_calls.0.function.name").String()
						} else {
							switch gjson.Get(payload, "type").String() {
							case "response.output_text.delta":
								text += gjson.Get(payload, "delta").String()
							case "response.function_call_arguments.delta":
								arguments += gjson.Get(payload, "delta").String()
							case "response.output_item.added":
								toolName += gjson.Get(payload, "item.name").String()
							}
						}
					}
				}
				require.Equal(t, tc.Text, text)
				if tc.ToolArguments != nil {
					require.Equal(t, *tc.ToolArguments, arguments)
					require.Equal(t, "Read", toolName)
				}
			})
		}
	}
}

// Native mR checks typeof delta.type === "string", not whether it is nonempty.
// Unknown string types are ignored; missing/null/non-string types fail.
func TestClaudeSSEDeclaredDeltaType(t *testing.T) {
	for _, tc := range []struct {
		delta     string
		wantError bool
	}{
		{`{"type":""}`, false}, {`{"type":"future_delta"}`, false},
		{`{}`, true}, {`{"type":null}`, true}, {`{"type":12}`, true},
	} {
		t.Run(tc.delta, func(t *testing.T) {
			wire := claudeTerminalFixture("complete")
			at := strings.Index(wire, "event: content_block_delta")
			wire = wire[:at] + "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":" + tc.delta + "}\n\n" + wire[at:]
			r := newAnthropicSSEReader(strings.NewReader(wire), 4096)
			for r.ScanEvent() {
			}
			if tc.wantError {
				require.Error(t, r.Err())
			} else {
				require.NoError(t, r.Err())
			}
		})
	}
}
