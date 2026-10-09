package requesttrace

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"net/http"
	"sync"
)

// Stream retains no body in memory. Base64 JSON chunks preserve invalid UTF-8,
// binary content and SSE framing exactly. Offsets detect missing rotated data.
type Stream struct {
	mu                       sync.Mutex
	trace                    *Trace
	name                     string
	attempt                  int64
	hash                     hash.Hash
	seen, captured, expected int64
	finished                 bool
	err                      string
}

func (t *Trace) Stream(name string, attempt, expected int64) *Stream {
	if t == nil {
		return nil
	}
	return &Stream{trace: t, name: name, attempt: attempt, hash: sha256.New(), expected: expected}
}

func (s *Stream) Write(p []byte) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return
	}
	_, _ = s.hash.Write(p)
	limit := s.trace.recorder.maxBodyBytes
	offset := s.seen
	s.seen += int64(len(p))
	if limit > 0 {
		remaining := limit - s.captured
		if remaining <= 0 {
			return
		}
		if int64(len(p)) > remaining {
			p = p[:remaining]
		}
	}
	for len(p) > 0 {
		n := min(len(p), 32*1024)
		if s.trace.Event("body.chunk", map[string]any{"stream": s.name, "attempt": s.attempt, "offset": offset, "base64": p[:n]}) {
			s.captured += int64(n)
		}
		offset += int64(n)
		p = p[n:]
	}
}

func (s *Stream) Finish(reason string, err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return
	}
	s.finished = true
	if err != nil {
		s.err = SafeError(err)
	}
	complete := (reason == "eof" || reason == "handler_return" || (s.expected >= 0 && s.seen == s.expected)) && s.err == ""
	s.trace.Event("body.end", map[string]any{
		"stream": s.name, "attempt": s.attempt, "bytes": s.seen, "captured_bytes": s.captured,
		"expected_bytes": s.expected, "sha256": hex.EncodeToString(s.hash.Sum(nil)),
		"complete": complete, "truncated": s.captured != s.seen, "reason": reason, "error": s.err,
	})
}

type capturedBody struct {
	io.ReadCloser
	stream *Stream
}

func CaptureBody(body io.ReadCloser, stream *Stream) io.ReadCloser {
	if stream == nil {
		return body
	}
	if body == nil {
		stream.Finish("eof", nil)
		return body
	}
	if body == http.NoBody {
		stream.Finish("eof", nil)
		return body
	}
	return &capturedBody{ReadCloser: body, stream: stream}
}

func (b *capturedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.stream.Write(p[:n])
	if err == io.EOF {
		b.stream.Finish("eof", nil)
	} else if err != nil {
		b.stream.Finish("read_error", err)
	}
	return n, err
}

func (b *capturedBody) Close() error {
	err := b.ReadCloser.Close()
	b.stream.Finish("closed", err)
	return err
}
