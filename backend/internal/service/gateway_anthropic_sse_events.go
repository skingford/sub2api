package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type anthropicSSEBlockState struct {
	open    bool
	changed bool
	start   *apicompat.AnthropicContentBlock
}

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
		if !json.Valid([]byte(r.data)) {
			r.err = errors.New("invalid upstream SSE JSON")
			return false
		}
		payload := gjson.Parse(r.data)
		if payload.Type == gjson.Null {
			r.err = errors.New("null upstream SSE event")
			return false
		}
		// The native application ignores non-null JSON values without a known
		// event type, including arrays and numbers. Do not turn them into errors
		// just because Go's typed decoder cannot unmarshal them into a struct.
		if !payload.IsObject() || payload.Get("type").Type != gjson.String {
			continue
		}
		switch payload.Get("type").Str {
		case "message_start", "message_delta", "message_stop", "content_block_start", "content_block_delta", "content_block_stop":
		default:
			continue
		}
		var event apicompat.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(r.data), &event); err != nil {
			r.err = fmt.Errorf("invalid upstream SSE JSON: %w", err)
			return false
		}
		keep, err := r.checkContentIndex(&event)
		if err != nil {
			r.err = err
			return false
		}
		if !keep {
			continue
		}
		r.message = &event
		return true
	}
	if r.err == nil && !r.terminal {
		r.err = fmt.Errorf("upstream SSE ended without a terminal signal: %w", io.ErrUnexpectedEOF)
	}
	return false
}

func (r *anthropicSSEReader) checkContentIndex(event *apicompat.AnthropicStreamEvent) (bool, error) {
	if event.Type != "message_start" && !r.started {
		return false, errors.New("upstream SSE event before message start")
	}
	switch event.Type {
	case "message_start":
		if event.Message == nil {
			return false, errors.New("upstream SSE message start without message")
		}
		if r.started && len(r.blocks) > 0 {
			return false, errors.New("upstream SSE message restart after content")
		}
		r.blocks = make([]anthropicSSEBlockState, len(event.Message.Content))
		r.started, r.terminal = true, false
	case "content_block_start":
		if event.ContentBlock == nil {
			return false, errors.New("upstream SSE content start without block")
		}
		if event.Index == nil || *event.Index < 0 || *event.Index > len(r.blocks) {
			return false, errors.New("invalid upstream SSE content block start index")
		}
		if *event.Index < len(r.blocks) {
			previous := r.blocks[*event.Index]
			// An identical restart before any delta has no observable content
			// change. Suppress its duplicate downstream item. A restart after
			// output cannot safely undo OpenAI deltas already sent to the client.
			if previous.open && !previous.changed && reflect.DeepEqual(previous.start, event.ContentBlock) {
				return false, nil
			}
			return false, errors.New("upstream SSE content restart after output or with changed block")
		}
		r.blocks = append(r.blocks, anthropicSSEBlockState{open: true, start: event.ContentBlock})
	case "content_block_delta", "content_block_stop":
		if event.Index == nil || *event.Index < 0 || *event.Index >= len(r.blocks) {
			return false, errors.New("invalid upstream SSE content block index")
		}
		block := &r.blocks[*event.Index]
		if !block.open {
			return false, errors.New("upstream SSE event for closed content block")
		}
		if event.Type == "content_block_stop" {
			block.open = false
			break
		}
		if event.Delta == nil || gjson.Get(r.data, "delta.type").Type != gjson.String {
			return false, errors.New("upstream SSE content delta without typed delta")
		}
		kind, field := "", ""
		switch event.Delta.Type {
		case "text_delta":
			kind, field = "text", "text"
		case "thinking_delta":
			if block.start.Type == "redacted_thinking" {
				return false, nil
			}
			kind, field = "thinking", "thinking"
		case "signature_delta":
			kind, field = "thinking", "signature"
		case "input_json_delta":
			kind, field = block.start.Type, "partial_json"
			if kind != "tool_use" && kind != "server_tool_use" {
				kind = "tool_use"
			}
		}
		if kind != "" && (block.start.Type != kind || gjson.Get(r.data, "delta."+field).Type != gjson.String) {
			return false, errors.New("upstream SSE delta does not match content block")
		}
		block.changed = true
	case "message_delta":
		if event.Delta == nil {
			return false, errors.New("upstream SSE message delta without delta")
		}
		if event.Delta.StopReason != "" {
			r.terminal = true
		}
	case "message_stop":
		r.terminal = true
	}
	return true, nil
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
