package claude

import (
	"bytes"
	"fmt"

	"github.com/cespare/xxhash/v2"
)

// CCH2292 replaces the native placeholder in the serialized body. Callers must
// enforce the version, endpoint and anthropic-version header gates. This is a
// checksum, not a credential or a provider-issued thinking signature.
// The independent 2.1.295 native-runtime oracle also matches this implementation;
// the historical function name identifies the algorithm, not a version wildcard.
//
// The raw-byte scan intentionally retains native quirks: no JSON reserialization,
// exact compact key spellings, and a 300-byte search window starting at system.
// Evidence: docs/claude-code-cch-2.1.292.md and the Docker runtime oracle vectors.
func CCH2292(body []byte) ([]byte, bool) {
	system := bytes.Index(body, []byte(`"system":[`))
	if system < 0 {
		return body, false
	}
	windowEnd := min(system+300, len(body))
	placeholder := bytes.Index(body[system:windowEnd], []byte("cch=00000"))
	if placeholder < 0 {
		return body, false
	}
	hash := xxhash.NewWithSeed(0x4D659218E32A3268)
	searches := [4]cchScan{
		{needle: []byte(`"model":"`)},
		{needle: []byte(`"fallbacks":[`)},
		{needle: []byte(`"fallback_credit_token":"`)},
		{needle: []byte(`"max_tokens":`)},
	}
	for cursor := 0; cursor < len(body); {
		// Cache each next match: advancing past many model keys must not
		// repeatedly scan the remainder of a large body for absent keys.
		for i := range searches {
			searches[i].advance(body, cursor, i)
		}
		start, end := len(body), len(body)
		if model := searches[0]; model.valid {
			start, end = model.start+len(model.needle), model.end
		}
		for _, field := range searches[1:] {
			if !field.valid {
				continue
			}
			s, e := field.start, field.end
			if e < len(body) && body[e] == ',' {
				e++
			} else if s > cursor && body[s-1] == ',' {
				s--
			}
			if s < start {
				start, end = s, e
			}
		}
		_, _ = hash.Write(body[cursor:start])
		cursor = end
	}
	out := bytes.Clone(body)
	copy(out[system+placeholder+4:], fmt.Sprintf("%05x", hash.Sum64()&0xfffff))
	return out, true
}

type cchScan struct {
	needle     []byte
	start, end int
	searched   bool
	valid      bool
}

func (s *cchScan) advance(body []byte, cursor, kind int) {
	if s.searched && (s.start == -1 || s.start >= cursor) {
		return
	}
	s.searched, s.valid = true, false
	for from := cursor; from < len(body); {
		relative := bytes.Index(body[from:], s.needle)
		if relative < 0 {
			s.start = -1
			return
		}
		s.start = from + relative
		value := s.start + len(s.needle)
		s.end = value
		switch kind {
		case 0, 2: // Native scans to the next quote, including escaped quotes.
			if quote := bytes.IndexByte(body[value:], '"'); quote >= 0 {
				s.end, s.valid = value+quote, true
				if kind == 2 {
					s.end++
				}
			}
		case 1:
			depth, quoted := 1, false
			for s.end < len(body) && depth > 0 {
				ch := body[s.end]
				s.end++
				if quoted {
					if ch == '\\' && s.end < len(body) {
						s.end++
					} else if ch == '"' {
						quoted = false
					}
				} else {
					switch ch {
					case '"':
						quoted = true
					case '[':
						depth++
					case ']':
						depth--
					}
				}
			}
			s.valid = depth == 0
		case 3:
			for s.end < len(body) && body[s.end] >= '0' && body[s.end] <= '9' {
				s.end++
			}
			s.valid = s.end > value
			if !s.valid {
				from = value
				continue
			}
		}
		return
	}
	s.start = -1
}
