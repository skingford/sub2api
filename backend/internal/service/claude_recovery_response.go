package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

// Hash only documented protocol representation differences. Tool arguments and
// tool-result data remain intact; a user field named cache_control is not stripped.
func recoveryMessageHash(raw json.RawMessage, route string) string {
	var m map[string]any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if dec.Decode(&m) != nil {
		return recoveryHash(raw)
	}
	var blocks func(any) []any
	blocks = func(v any) []any {
		if text, ok := v.(string); ok {
			if text == "" {
				return []any{}
			}
			return []any{map[string]any{"type": "text", "text": text}}
		}
		values, _ := v.([]any)
		out := make([]any, 0, len(values))
		for _, value := range values {
			b, ok := value.(map[string]any)
			if !ok {
				out = append(out, value)
				continue
			}
			if b["type"] == "thinking" || b["type"] == "redacted_thinking" {
				continue
			}
			delete(b, "cache_control")
			for _, key := range []string{"citations", "annotations"} {
				if x, exists := b[key]; exists {
					if x == nil {
						delete(b, key)
					} else if a, ok := x.([]any); ok && len(a) == 0 {
						delete(b, key)
					}
				}
			}
			if b["type"] == "input_text" || b["type"] == "output_text" {
				b["type"] = "text"
			}
			if b["type"] == "tool_result" {
				b["content"] = blocks(b["content"])
				if b["is_error"] == false {
					delete(b, "is_error")
				}
			}
			out = append(out, b)
		}
		return out
	}
	if role, ok := m["role"].(string); ok {
		out := map[string]any{"role": role, "content": blocks(m["content"])}
		if calls, ok := m["tool_calls"].([]any); ok && len(calls) > 0 {
			out["tool_calls"] = calls
		}
		if name, ok := m["name"].(string); ok && name != "" {
			out["name"] = name
		}
		if id, ok := m["tool_call_id"].(string); ok && id != "" {
			out["tool_call_id"] = id
		}
		if text, ok := m["reasoning_content"].(string); ok && text != "" {
			out["reasoning_content"] = text
		}
		m = out
	} else if route == "responses" {
		delete(m, "id")
		delete(m, "status")
	}
	encoded, e := json.Marshal(m)
	if e != nil {
		return recoveryHash(raw)
	}
	return recoveryHash(encoded)
}

func recoveryCompletedHistory(h RecoveryHistory, response []byte, stream bool) (RecoveryHistory, error) {
	var messages []json.RawMessage
	if !stream {
		if !gjson.ValidBytes(response) {
			return h, ErrRecoveryUncertain
		}
		switch h.Route {
		case "messages":
			if gjson.GetBytes(response, "role").String() != "assistant" || !gjson.GetBytes(response, "content").IsArray() {
				return h, ErrRecoveryUncertain
			}
			msg, e := json.Marshal(map[string]any{"role": "assistant", "content": json.RawMessage(gjson.GetBytes(response, "content").Raw)})
			if e != nil {
				return h, e
			}
			messages = append(messages, msg)
		case "chat":
			choices := gjson.GetBytes(response, "choices").Array()
			if len(choices) != 1 || choices[0].Get("message.role").String() != "assistant" {
				return h, ErrRecoveryUncertain
			}
			messages = append(messages, json.RawMessage(choices[0].Get("message").Raw))
		case "responses":
			if gjson.GetBytes(response, "status").String() != "completed" {
				return h, ErrRecoveryUncertain
			}
			for _, item := range gjson.GetBytes(response, "output").Array() {
				messages = append(messages, json.RawMessage(item.Raw))
			}
		default:
			return h, ErrRecoveryConflict
		}
	} else {
		blocks := map[int]map[string]any{}
		partial := map[int]string{}
		text, reasoning := "", ""
		calls := map[int]map[string]any{}
		done := false
		for _, frame := range strings.Split(strings.ReplaceAll(string(response), "\r\n", "\n"), "\n\n") {
			var lines []string
			for _, line := range strings.Split(frame, "\n") {
				if strings.HasPrefix(line, "data:") {
					lines = append(lines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
				}
			}
			data := strings.Join(lines, "\n")
			if data == "" {
				continue
			}
			if data == "[DONE]" {
				done = true
				continue
			}
			if !gjson.Valid(data) {
				return h, ErrRecoveryUncertain
			}
			event := gjson.Parse(data)
			switch h.Route {
			case "messages":
				index := int(event.Get("index").Int())
				switch event.Get("type").String() {
				case "message_start":
					for i, b := range event.Get("message.content").Array() {
						var v map[string]any
						if e := json.Unmarshal([]byte(b.Raw), &v); e != nil {
							return h, e
						}
						blocks[i] = v
					}
				case "content_block_start":
					var b map[string]any
					if e := json.Unmarshal([]byte(event.Get("content_block").Raw), &b); e != nil {
						return h, e
					}
					blocks[index] = b
				case "content_block_delta":
					b := blocks[index]
					if b == nil {
						return h, ErrRecoveryUncertain
					}
					delta := event.Get("delta")
					switch delta.Get("type").String() {
					case "text_delta":
						old, _ := b["text"].(string)
						b["text"] = old + delta.Get("text").String()
					case "thinking_delta":
						old, _ := b["thinking"].(string)
						b["thinking"] = old + delta.Get("thinking").String()
					case "signature_delta":
						old, _ := b["signature"].(string)
						b["signature"] = old + delta.Get("signature").String()
					case "input_json_delta":
						partial[index] += delta.Get("partial_json").String()
					case "citations_delta":
						old, _ := b["citations"].([]any)
						b["citations"] = append(old, delta.Get("citation").Value())
					default:
						return h, fmt.Errorf("unsupported managed stream delta")
					}
				case "content_block_stop":
					if part := partial[index]; part != "" {
						var value any
						if e := json.Unmarshal([]byte(part), &value); e != nil {
							return h, e
						}
						blocks[index]["input"] = value
					}
				case "message_stop":
					done = true
				case "error":
					return h, ErrRecoveryUncertain
				}
			case "chat":
				for _, choice := range event.Get("choices").Array() {
					if choice.Get("index").Int() != 0 {
						return h, ErrRecoveryConflict
					}
					d := choice.Get("delta")
					text += d.Get("content").String()
					reasoning += d.Get("reasoning_content").String()
					for _, call := range d.Get("tool_calls").Array() {
						i := int(call.Get("index").Int())
						entry := calls[i]
						if entry == nil {
							entry = map[string]any{"type": "function", "id": "", "function": map[string]any{"name": "", "arguments": ""}}
							calls[i] = entry
						}
						if id := call.Get("id").String(); id != "" {
							entry["id"] = id
						}
						fn, ok := entry["function"].(map[string]any)
						if !ok {
							return h, ErrRecoveryUncertain
						}
						for _, key := range []string{"name", "arguments"} {
							if delta := call.Get("function." + key).String(); delta != "" {
								prior, ok := fn[key].(string)
								if !ok {
									return h, ErrRecoveryUncertain
								}
								fn[key] = prior + delta
							}
						}
					}
				}
			case "responses":
				if event.Get("type").String() == "response.completed" {
					done = true
					for _, item := range event.Get("response.output").Array() {
						messages = append(messages, json.RawMessage(item.Raw))
					}
				}
				if event.Get("type").String() == "response.failed" || event.Get("type").String() == "error" {
					return h, ErrRecoveryUncertain
				}
			}
		}
		if !done {
			return h, ErrRecoveryUncertain
		}
		if h.Route == "messages" {
			content := make([]any, len(blocks))
			for i := range content {
				if blocks[i] == nil {
					return h, ErrRecoveryUncertain
				}
				content[i] = blocks[i]
			}
			msg, e := json.Marshal(map[string]any{"role": "assistant", "content": content})
			if e != nil {
				return h, e
			}
			messages = append(messages, msg)
		}
		if h.Route == "chat" {
			msg := map[string]any{"role": "assistant", "content": text}
			if reasoning != "" {
				msg["reasoning_content"] = reasoning
			}
			if len(calls) > 0 {
				list := make([]any, len(calls))
				for i := range list {
					if calls[i] == nil {
						return h, ErrRecoveryUncertain
					}
					list[i] = calls[i]
				}
				msg["tool_calls"] = list
			}
			raw, e := json.Marshal(msg)
			if e != nil {
				return h, e
			}
			messages = append(messages, raw)
		}
	}
	if len(messages) == 0 {
		return h, ErrRecoveryUncertain
	}
	h.Messages = append(append([]json.RawMessage(nil), h.Messages...), messages...)
	return h, nil
}
