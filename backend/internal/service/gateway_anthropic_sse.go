package service

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

// anthropicSSEReader assembles an event before protocol conversion. CLI SDK
// 0.128.0 accepts comments, arbitrary field order, repeated data fields, and
// LF/CRLF/CR line endings. The bound applies to the whole frame, so many short
// data (or ignored) lines cannot bypass the existing response size guard.
type anthropicSSEReader struct {
	scanner  *bufio.Scanner
	limit    int
	event    string
	data     string
	err      error
	message  *apicompat.AnthropicStreamEvent
	blocks   []anthropicSSEBlockState
	started  bool
	terminal bool
}

func newAnthropicSSEReader(r io.Reader, limit int) *anthropicSSEReader {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, min(64*1024, limit)), limit)
	scanner.Split(splitAnthropicSSELine)
	return &anthropicSSEReader{scanner: scanner, limit: limit}
}

func (r *anthropicSSEReader) Scan() bool {
	if r.err != nil {
		return false
	}
	r.event, r.data = "", ""
	var data strings.Builder
	size, fields := 0, 0
	for r.scanner.Scan() {
		line := r.scanner.Text()
		if line == "" {
			if r.event != "" || fields > 0 {
				r.data = data.String()
				return true
			}
			size = 0
			continue
		}
		if len(line)+1 > r.limit-size {
			r.err = fmt.Errorf("upstream SSE event exceeded %d bytes", r.limit)
			return false
		}
		size += len(line) + 1
		if strings.HasPrefix(line, ":") {
			continue
		}
		if value, ok := parseAnthropicSSEField(line, "event"); ok {
			r.event = value
		} else if value, ok := parseAnthropicSSEField(line, "data"); ok {
			if fields > 0 {
				_ = data.WriteByte('\n')
			}
			_, _ = data.WriteString(value)
			fields++
		}
	}
	r.err = r.scanner.Err()
	// Preserve the gateway's existing tolerance for a final event without a
	// blank separator at clean EOF. Never dispatch a frame after a read error.
	if r.err == nil && fields > 0 {
		r.data = data.String()
		return true
	}
	return false
}

func (r *anthropicSSEReader) Err() error { return r.err }

// SSE removes exactly one ASCII space after the colon. A field without a
// colon has an empty value; additional spaces and tabs are data, not padding.
func parseAnthropicSSEField(line, field string) (string, bool) {
	key, value, _ := strings.Cut(line, ":")
	if key != field {
		return "", false
	}
	return strings.TrimPrefix(value, " "), true
}

func splitAnthropicSSELine(data []byte, atEOF bool) (int, []byte, error) {
	i := bytes.IndexAny(data, "\r\n")
	if i >= 0 {
		advance := i + 1
		if data[i] == '\r' {
			if advance == len(data) && !atEOF {
				return 0, nil, nil // CRLF may straddle two reads.
			}
			if advance < len(data) && data[advance] == '\n' {
				advance++
			}
		}
		return advance, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
