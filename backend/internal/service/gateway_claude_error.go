package service

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

const claudeInvalidJSONMessage = "The request body is not valid JSON"

func claudeInvalidJSONMessagePrefix(message string) bool {
	return strings.HasPrefix(message, claudeInvalidJSONMessage) ||
		strings.HasPrefix(message, "Failed to parse request: could not parse request body as JSON")
}

// claudeInvalidJSONRejection matches te() in the pinned 2.1.292 CLI. It inspects
// at most the same 8192 wire-decoded bytes, with JSON schema precedence intact.
// In particular, a quoted JSON string is not a plaintext parse error, and an
// ordinary error must not become retryable just because we trim/unwrap it.
func claudeInvalidJSONRejection(body []byte) bool {
	body = body[:min(len(body), 8192)]
	if len(body) > 0 {
		// The native streaming TextDecoder is not flushed at the end of its
		// bounded read; an incomplete final UTF-8 sequence remains buffered.
		last := len(body) - 1
		for last > 0 && !utf8.RuneStart(body[last]) {
			last--
		}
		if !utf8.FullRune(body[last:]) {
			body = body[:last]
		}
	}
	body = bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf})
	if !json.Valid(body) {
		return claudeBadJSONText(strings.TrimRightFunc(string(body), claudeJSWhitespace))
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(body))
	// Native JSON.parse accepts large numbers in unrelated fields. Retain
	// numeric lexemes so a float64 overflow cannot change retry classification.
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return false
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return false
	}
	if object["type"] == "error" {
		if detail, ok := object["error"].(map[string]any); ok {
			_, typeOK := detail["type"].(string)
			message, messageOK := detail["message"].(string)
			if typeOK && messageOK {
				return claudeInvalidJSONMessagePrefix(message)
			}
		}
	}
	message, ok := object["error"].(string)
	return ok && object["reason"] == "bad_json" && claudeBadJSONText(message)
}

func claudeBadJSONText(message string) bool {
	return strings.HasPrefix(message, "bad json: invalid character ") ||
		message == "bad json: unexpected EOF" ||
		message == "bad json: unexpected end of JSON input" ||
		message == "bad json: EOF"
}

// ECMAScript trimEnd: unlike unicode.IsSpace, this includes BOM but not NEL.
func claudeJSWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', '\ufeff', '\u2028', '\u2029':
		return true
	default:
		return unicode.Is(unicode.Zs, r)
	}
}
