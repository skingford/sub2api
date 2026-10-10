package apicompat

import (
	"encoding/json"
	"fmt"
)

// ChatCompletionsToAnthropicRequest keeps Anthropic-bound constraints out of
// the OpenAI Responses wire contract. In particular, Responses' token floor
// and lack of stop must not change a Chat caller's explicit generation limits.
// Callers resolve the final model before invoking this converter.
func ChatCompletionsToAnthropicRequest(req *ChatCompletionsRequest) (*AnthropicRequest, error) {
	converted, err := chatCompletionsToResponses(req, true)
	if err != nil {
		return nil, err
	}
	limit := req.MaxTokens
	if req.MaxCompletionTokens != nil {
		limit = req.MaxCompletionTokens
	}
	if limit != nil {
		if *limit <= 0 {
			return nil, fmt.Errorf("max_tokens / max_completion_tokens must be positive")
		}
		value := *limit
		converted.MaxOutputTokens = &value
	}
	stops, err := chatStopSequences(req.Stop)
	if err != nil {
		return nil, err
	}
	out, err := ResponsesToAnthropicRequest(converted)
	if err != nil {
		return nil, err
	}
	out.StopSeqs = stops
	return out, nil
}

func chatStopSequences(raw json.RawMessage) ([]string, error) {
	raw = normalizedRawJSON(raw)
	if len(raw) == 0 {
		return nil, nil
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return []string{single}, nil
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("stop must be a string or an array of strings")
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		var text string
		if len(value) == 0 || value[0] != '"' || json.Unmarshal(value, &text) != nil {
			return nil, fmt.Errorf("stop must contain only strings")
		}
		out = append(out, text)
	}
	return out, nil
}

// OpenAI's name/strict wrapper is not an Anthropic field. Preserve the actual
// schema verbatim instead of rewriting its constraints or dropping its format.
func responsesFormatToAnthropic(text *ResponsesText) (json.RawMessage, error) {
	if text == nil {
		return nil, nil
	}
	raw := normalizedRawJSON(text.Format)
	if len(raw) == 0 {
		return nil, nil
	}
	format, ok := rawJSONObject(raw)
	if !ok {
		return nil, fmt.Errorf("text.format must be an object")
	}
	switch rawString(format["type"]) {
	case "text":
		return nil, nil
	case "json_schema":
		schema := normalizedRawJSON(format["schema"])
		if _, ok := rawJSONObject(schema); len(schema) == 0 || !ok || schema[0] != '{' {
			return nil, fmt.Errorf("json_schema format requires a schema object")
		}
		return json.Marshal(map[string]json.RawMessage{"type": rawJSONString("json_schema"), "schema": schema})
	default:
		return nil, fmt.Errorf("unsupported Anthropic output format %q; use text or json_schema", rawString(format["type"]))
	}
}

func setAnthropicOutputEffort(out *AnthropicRequest, effort string) {
	if out.OutputConfig == nil {
		out.OutputConfig = &AnthropicOutputConfig{}
	}
	out.OutputConfig.Effort = effort
}
