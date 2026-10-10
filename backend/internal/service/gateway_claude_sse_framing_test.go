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

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// The independent CLI lab uses the same content/usage oracle and framing cases.
// These assertions exercise the real four downstream conversion handlers.
func TestClaudeSSEFramingHandlers(t *testing.T) {
	const marker = "LOCAL_SSE_中文_🙂"
	events := []string{
		`{"type":"message_start","message":{"id":"msg_framing","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"usage":{"input_tokens":17,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"LOCAL_SSE_中文_🙂"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":9}}`,
		`{"type":"message_stop"}`,
	}
	for _, framing := range []string{"standard", "comments", "data-first", "multiline", "crlf", "cr", "fragmented"} {
		var wire strings.Builder
		for _, raw := range events {
			event := "event: " + gjson.Get(raw, "type").String()
			if framing == "multiline" {
				var value any
				require.NoError(t, json.Unmarshal([]byte(raw), &value))
				pretty, err := json.MarshalIndent(value, "", "  ")
				require.NoError(t, err)
				raw = string(pretty)
			}
			data := "data: " + strings.ReplaceAll(raw, "\n", "\ndata: ")
			frame := event + "\n" + data + "\n\n"
			switch framing {
			case "comments":
				frame = event + "\n: keepalive\nid: local\nretry: 1\n" + data + "\n\n"
			case "data-first":
				frame = data + "\n" + event + "\n\n"
			case "crlf":
				frame = strings.ReplaceAll(frame, "\n", "\r\n")
			case "cr":
				frame = strings.ReplaceAll(frame, "\n", "\r")
			}
			wire.WriteString(frame)
		}
		// Optional lab replay uses the exact synthetic response bytes already
		// checked against PCAP and accepted by the unmodified pinned CLI.
		if root := os.Getenv("CLAUDE_SSE_CAPTURE_ROOT"); root != "" {
			raw, err := os.ReadFile(filepath.Join(root, framing, "responses.json"))
			require.NoError(t, err)
			var responses []struct {
				Body string `json:"body"`
			}
			require.NoError(t, json.Unmarshal(raw, &responses))
			require.Len(t, responses, 1)
			wire.Reset()
			wire.WriteString(responses[0].Body)
		}
		for _, path := range []string{"responses-buffered", "responses-stream", "chat-buffered", "chat-stream"} {
			t.Run(framing+"/"+path, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				var reader io.Reader = strings.NewReader(wire.String())
				if framing == "fragmented" {
					reader = &claudeSSEByteReader{reader}
				}
				resp := &http.Response{Header: make(http.Header), Body: io.NopCloser(reader)}
				svc := &GatewayService{}
				var result *ForwardResult
				var err error
				switch path {
				case "responses-buffered":
					result, err = svc.handleResponsesBufferedStreamingResponse(resp, c, "claude-sonnet-4-6", "claude-sonnet-4-6", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
				case "responses-stream":
					result, err = svc.handleResponsesStreamingResponse(resp, c, "claude-sonnet-4-6", "claude-sonnet-4-6", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
				case "chat-buffered":
					result, err = svc.handleCCBufferedFromAnthropic(resp, c, "claude-sonnet-4-6", "claude-sonnet-4-6", nil, time.Now())
				case "chat-stream":
					result, err = svc.handleCCStreamingFromAnthropic(resp, c, "claude-sonnet-4-6", "claude-sonnet-4-6", nil, time.Now())
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 17, result.Usage.InputTokens)
				require.Equal(t, 9, result.Usage.OutputTokens)
				if path == "responses-buffered" {
					require.Equal(t, marker, gjson.GetBytes(rec.Body.Bytes(), "output.0.content.0.text").String())
				} else if path == "chat-buffered" {
					require.Equal(t, marker, gjson.GetBytes(rec.Body.Bytes(), "choices.0.message.content").String())
				} else {
					var text strings.Builder
					for _, line := range strings.Split(rec.Body.String(), "\n") {
						if !strings.HasPrefix(line, "data: ") {
							continue
						}
						payload := strings.TrimPrefix(line, "data: ")
						if path == "chat-stream" {
							text.WriteString(gjson.Get(payload, "choices.0.delta.content").String())
						} else if gjson.Get(payload, "type").String() == "response.output_text.delta" {
							text.WriteString(gjson.Get(payload, "delta").String())
						}
					}
					require.Equal(t, marker, text.String())
				}
			})
		}
	}
}

type claudeSSEByteReader struct{ io.Reader }

func (r *claudeSSEByteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}

func TestClaudeSSEFramingReadErrors(t *testing.T) {
	const prefix = "event: message_start\ndata: " + `{"type":"message_start","message":{"id":"msg_partial","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"usage":{"input_tokens":17}}}` + "\n\n"
	for _, failure := range []string{"read-error", "long-line", "large-frame"} {
		for _, path := range []string{"responses-buffered", "responses-stream", "chat-buffered", "chat-stream"} {
			t.Run(failure+"/"+path, func(t *testing.T) {
				var reader io.Reader
				switch failure {
				case "read-error":
					reader = io.MultiReader(strings.NewReader(prefix+"data: pending\n"), claudeSSEFailReader{})
				case "long-line":
					reader = strings.NewReader(prefix + "data: " + strings.Repeat("x", 600) + "\n\n")
				case "large-frame":
					reader = strings.NewReader(prefix + strings.Repeat("data: short\n", 60) + "\n")
				}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				resp := &http.Response{Header: make(http.Header), Body: io.NopCloser(reader)}
				svc := &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: 512}}}
				var result *ForwardResult
				var err error
				switch path {
				case "responses-buffered":
					result, err = svc.handleResponsesBufferedStreamingResponse(resp, c, "claude-sonnet-4-6", "claude-sonnet-4-6", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
				case "responses-stream":
					result, err = svc.handleResponsesStreamingResponse(resp, c, "claude-sonnet-4-6", "claude-sonnet-4-6", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
				case "chat-buffered":
					result, err = svc.handleCCBufferedFromAnthropic(resp, c, "claude-sonnet-4-6", "claude-sonnet-4-6", nil, time.Now())
				case "chat-stream":
					result, err = svc.handleCCStreamingFromAnthropic(resp, c, "claude-sonnet-4-6", "claude-sonnet-4-6", nil, time.Now())
				}
				require.Error(t, err)
				require.True(t, IsResponseCommitted(c))
				require.Contains(t, rec.Body.String(), `"error"`)
				require.NotContains(t, rec.Body.String(), "response.completed")
				require.NotContains(t, rec.Body.String(), "[DONE]")
				if strings.HasSuffix(path, "buffered") {
					require.Nil(t, result)
					require.Equal(t, http.StatusBadGateway, rec.Code)
				} else {
					require.NotNil(t, result)
					require.Equal(t, 17, result.Usage.InputTokens)
				}
			})
		}
	}
}
