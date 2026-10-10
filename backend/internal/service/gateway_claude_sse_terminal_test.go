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

func claudeTerminalFixture(control string) string {
	events := []string{
		`{"type":"message_start","message":{"id":"msg_terminal","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"usage":{"input_tokens":17,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"LOCAL_TERMINAL_中文_🙂"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":9}}`,
		`{"type":"message_stop"}`,
	}
	frame := func(event string) string {
		return "event: " + gjson.Get(event, "type").String() + "\ndata: " + event + "\n\n"
	}
	frames := make([]string, len(events))
	for i, event := range events {
		frames[i] = frame(event)
	}
	insert := func(at int, value string) { frames = append(frames[:at], append([]string{value}, frames[at:]...)...) }
	errorFrame := frame(`{"type":"error","error":{"type":"invalid_request_error","message":"LOCAL_SYNTHETIC_STREAM_ERROR"}}`)
	switch control {
	case "error-before":
		frames = []string{errorFrame}
	case "error-after":
		frames = append(frames[:3], errorFrame)
	case "invalid-json":
		insert(2, "event: content_block_delta\ndata: {invalid\n\n")
	case "missing-stop":
		frames = frames[:5]
	case "missing-delta":
		frames = append(frames[:4], frames[5])
	case "partial-content":
		frames = frames[:3]
	case "unknown-event":
		insert(2, "event: local_future_event\ndata: "+strings.ReplaceAll(events[2], "LOCAL_TERMINAL_中文_🙂", "SHOULD_BE_IGNORED")+"\n\n")
	case "unknown-json":
		insert(2, "event: local_future_event\ndata: {invalid\n\n")
	case "negative-index":
		insert(2, frame(strings.Replace(events[2], `"index":0`, `"index":-1`, 1)))
	case "delta-before-start":
		insert(1, frames[2])
	}
	return strings.Join(frames, "")
}

func TestClaudeSSETerminalContract(t *testing.T) {
	controls := []string{"complete", "error-before", "error-after", "invalid-json", "missing-stop", "missing-delta", "partial-content", "unknown-event", "unknown-json", "negative-index", "delta-before-start"}
	for _, control := range controls {
		wire := claudeTerminalFixture(control)
		if root := os.Getenv("CLAUDE_SSE_TERMINAL_CAPTURE_ROOT"); root != "" {
			raw, err := os.ReadFile(filepath.Join(root, control, "responses.json"))
			require.NoError(t, err)
			var responses []struct {
				Body string `json:"body"`
			}
			require.NoError(t, json.Unmarshal(raw, &responses))
			require.Len(t, responses, 1)
			wire = responses[0].Body
		}
		wantFailure := control == "error-before" || control == "error-after" || control == "invalid-json" || control == "partial-content" || control == "negative-index" || control == "delta-before-start"
		for _, target := range []string{"responses-buffered", "responses-stream", "chat-buffered", "chat-stream"} {
			t.Run(control+"/"+target, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				resp := &http.Response{Header: make(http.Header), Body: io.NopCloser(strings.NewReader(wire))}
				svc := &GatewayService{}
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
				if wantFailure {
					require.Error(t, err)
					require.True(t, IsResponseCommitted(c))
					require.Contains(t, rec.Body.String(), `"error"`)
					if control == "error-before" || control == "error-after" {
						require.Contains(t, rec.Body.String(), "LOCAL_SYNTHETIC_STREAM_ERROR")
					}
					require.NotContains(t, rec.Body.String(), "response.completed")
					require.NotContains(t, rec.Body.String(), "[DONE]")
					if strings.HasSuffix(target, "buffered") {
						require.Equal(t, http.StatusBadGateway, rec.Code)
					}
					return
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 17, result.Usage.InputTokens)
				wantTokens := 9
				if control == "missing-delta" {
					wantTokens = 0
				}
				require.Equal(t, wantTokens, result.Usage.OutputTokens)
				text := ""
				if target == "responses-buffered" {
					text = gjson.GetBytes(rec.Body.Bytes(), "output.0.content.0.text").String()
				} else if target == "chat-buffered" {
					text = gjson.GetBytes(rec.Body.Bytes(), "choices.0.message.content").String()
				} else {
					for _, line := range strings.Split(rec.Body.String(), "\n") {
						if !strings.HasPrefix(line, "data: ") {
							continue
						}
						payload := strings.TrimPrefix(line, "data: ")
						if target == "chat-stream" {
							text += gjson.Get(payload, "choices.0.delta.content").String()
						} else if gjson.Get(payload, "type").String() == "response.output_text.delta" {
							text += gjson.Get(payload, "delta").String()
						}
					}
				}
				require.Equal(t, "LOCAL_TERMINAL_中文_🙂", text)
			})
		}
	}
}
