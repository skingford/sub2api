package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
)

// ScanEvent applies Messages event semantics after framing. In SDK 0.128.0 the
// SSE event name controls dispatch: unknown events and pings must not inject a
// recognized body type, and malformed known events must not become success.
func (r *anthropicSSEReader) ScanEvent() bool {
	for r.Scan() {
		switch r.event {
		case "error":
			r.err = &sseStreamErrorEventError{RawData: r.data}
			return false
		case "message_start", "message_delta", "message_stop", "content_block_start", "content_block_delta", "content_block_stop":
		default:
			continue
		}
		var event apicompat.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(r.data), &event); err != nil {
			r.err = fmt.Errorf("invalid upstream SSE JSON: %w", err)
			return false
		}
		if err := r.checkContentIndex(&event); err != nil {
			r.err = err
			return false
		}
		r.message = &event
		return true
	}
	if r.err == nil && !r.terminal {
		for _, open := range r.blocks {
			if open {
				r.err = fmt.Errorf("upstream SSE ended inside a content block: %w", io.ErrUnexpectedEOF)
				break
			}
		}
	}
	return false
}

func (r *anthropicSSEReader) checkContentIndex(event *apicompat.AnthropicStreamEvent) error {
	switch event.Type {
	case "message_start":
		if event.Message != nil {
			r.blocks = make([]bool, len(event.Message.Content))
			r.terminal = false
		}
	case "content_block_start":
		if event.Index == nil || *event.Index != len(r.blocks) {
			return errors.New("invalid upstream SSE content block start index")
		}
		r.blocks = append(r.blocks, true)
	case "content_block_delta", "content_block_stop":
		if event.Index == nil || *event.Index < 0 || *event.Index >= len(r.blocks) {
			return errors.New("invalid upstream SSE content block index")
		}
		if event.Type == "content_block_stop" {
			r.blocks[*event.Index] = false
		}
	case "message_delta":
		if event.Delta != nil && event.Delta.StopReason != "" {
			r.terminal = true
		}
	case "message_stop":
		r.terminal = true
	}
	return nil
}

// Converted endpoints retain their existing OpenAI server_error shape. Only a
// complete upstream error envelope supplies its message; parse/transport errors
// use a fixed message and never reflect raw JSON fragments or network details.
func writeConvertedAnthropicSSEError(c *gin.Context, err error, format claudeErrorFormat, stream bool) {
	message := "Upstream stream could not be read"
	var upstream *sseStreamErrorEventError
	if errors.As(err, &upstream) {
		message = "Claude upstream returned a stream error"
		var body struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(upstream.RawData), &body) == nil {
			if detail := strings.TrimSpace(body.Error.Message); detail != "" {
				message = sanitizeUpstreamErrorMessage(detail)
			}
		}
	}
	if !stream {
		if format == claudeErrorChat {
			writeGatewayCCError(c, http.StatusBadGateway, "server_error", message)
		} else {
			writeResponsesError(c, http.StatusBadGateway, "server_error", message)
		}
		return
	}
	MarkResponseCommitted(c)
	if format == claudeErrorChat {
		payload, _ := json.Marshal(gin.H{"error": gin.H{"type": "server_error", "message": message}})
		_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", payload)
	} else {
		payload, _ := json.Marshal(gin.H{"type": "error", "code": "server_error", "message": message})
		_, _ = fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", payload)
	}
	c.Writer.Flush()
}
