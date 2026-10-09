//go:build unit

package service

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"os"
	"strings"
	"testing"
)

func TestPostfixEntrypointControls(t *testing.T) {
	path := os.Getenv("CLAUDE_POSTFIX_CONTROLS_OUTPUT")
	if path == "" {
		t.Skip("audit output required")
	}
	schema := map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}, "required": []string{"ok"}, "additionalProperties": false}
	var rows []map[string]any
	for _, model := range []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5-20251001", "claude-sonnet-5-5", "claude-opus-5-5", "claude-haiku-5-5"} {
		for _, route := range []string{"/v1/messages", "/v1/chat/completions", "/v1/responses"} {
			for _, control := range []string{"default", "format", "max4096", "max64", "effort-xhigh", "stop"} {
				if control == "stop" && route == "/v1/responses" {
					continue
				}
				input := map[string]any{"model": model}
				if route == "/v1/responses" {
					input["input"] = "LOCAL_POSTFIX_AUDIT"
				} else {
					input["messages"] = []any{map[string]any{"role": "user", "content": "LOCAL_POSTFIX_AUDIT"}}
				}
				switch control {
				case "format":
					if route == "/v1/messages" {
						input["output_config"] = map[string]any{"format": map[string]any{"type": "json_schema", "schema": schema}}
					} else if route == "/v1/responses" {
						input["text"] = map[string]any{"format": map[string]any{"type": "json_schema", "name": "audit", "strict": true, "schema": schema}}
					} else {
						input["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "audit", "strict": true, "schema": schema}}
					}
				case "stop":
					if route == "/v1/messages" {
						input["stop_sequences"] = []string{"STOP_AUDIT"}
					} else {
						input["stop"] = []string{"STOP_AUDIT"}
					}
				case "max4096", "max64":
					key := "max_tokens"
					if route == "/v1/responses" {
						key = "max_output_tokens"
					}
					input[key] = 4096
					if control == "max64" {
						input[key] = 64
					}
				case "effort-xhigh":
					if route == "/v1/messages" {
						input["output_config"] = map[string]any{"effort": "xhigh"}
					} else if route == "/v1/responses" {
						input["reasoning"] = map[string]any{"effort": "xhigh"}
					} else {
						input["reasoning_effort"] = "xhigh"
					}
				}
				raw, e := json.Marshal(input)
				require.NoError(t, e)
				svc, up := newClaudeContractGateway(t)
				headers := map[string]string{}
				if route == "/v1/messages" && control == "format" {
					headers["anthropic-beta"] = "structured-outputs-2025-12-15"
				}
				_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), raw, headers, route)
				fields := map[string]any{}
				for _, key := range []string{"model", "max_tokens", "thinking", "temperature", "output_config", "context_management", "stop_sequences"} {
					fields[key] = gjson.GetBytes(up.body, key).Value()
				}
				row := map[string]any{"model": model, "route": route, "control": control, "status": rec.Code, "calls": up.calls, "fields": fields, "input": input}
				if err != nil {
					row["error"] = err.Error()
				}
				if up.request != nil {
					row["beta"] = getHeaderRaw(up.request.Header, "anthropic-beta")
					row["ua"] = getHeaderRaw(up.request.Header, "user-agent")
					row["body"] = string(up.body)
				}
				row["schema_present"] = gjson.GetBytes(up.body, "output_config.format.schema.properties.ok.type").String() == "boolean"
				row["stop_present"] = strings.Contains(gjson.GetBytes(up.body, "stop_sequences").Raw, "STOP_AUDIT")
				rows = append(rows, row)
			}
		}
	}
	b, e := json.MarshalIndent(rows, "", "  ")
	require.NoError(t, e)
	require.NoError(t, os.WriteFile(path, append(b, '\n'), 0600))
}
