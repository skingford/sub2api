//go:build unit

// Load with go -overlay into internal/service. This emits observations; a
// successful test process does not mean every client constraint was retained.
package service

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClaudeToolConstraintAudit(t *testing.T) {
	folder := os.Getenv("CLAUDE_TOOL_CONSTRAINT_AUDIT")
	if folder == "" {
		t.Skip("isolated audit output required")
	}
	require.NoError(t, os.MkdirAll(filepath.Join(folder, "exports"), 0700))
	models := []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5-20251001", "claude-sonnet-5-5", "claude-opus-5-5", "claude-haiku-5-5"}
	schema := map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []string{"value"}, "additionalProperties": false}
	controls := []string{"parallel-false-auto", "parallel-false-default", "parallel-true-auto", "strict-true", "strict-false", "strict-parallel-schema", "max64-high", "max1024-high", "max1025-high", "max4096-high", "max64-none", "max64-low", "named-tool", "required-tool"}
	var rows []map[string]any
	for _, model := range models {
		for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, route := range []string{"/v1/messages", "/v1/chat/completions", "/v1/responses"} {
				for _, control := range controls {
					input := map[string]any{"model": model}
					limitKey := "max_tokens"
					if route == "/v1/responses" {
						input["input"] = "LOCAL_TOOL_CONSTRAINT_AUDIT"
						limitKey = "max_output_tokens"
					} else {
						input["messages"] = []any{map[string]any{"role": "user", "content": "LOCAL_TOOL_CONSTRAINT_AUDIT"}}
					}
					input[limitKey] = 4096
					tool := map[string]any{"name": "audit_tool", "description": "Local inert audit tool"}
					if route == "/v1/messages" {
						tool["input_schema"] = schema
					} else {
						tool["parameters"] = schema
					}
					if strings.Contains(control, "strict") {
						tool["strict"] = control != "strict-false"
					}
					if route == "/v1/chat/completions" {
						input["tools"] = []any{map[string]any{"type": "function", "function": tool}}
					} else {
						if route == "/v1/responses" {
							tool["type"] = "function"
						}
						input["tools"] = []any{tool}
					}
					choice := "auto"
					if control == "named-tool" {
						choice = "named"
					}
					if control == "required-tool" {
						choice = "required"
					}
					if control != "parallel-false-default" {
						if route == "/v1/messages" {
							tc := map[string]any{"type": choice}
							if choice == "named" {
								tc["type"] = "tool"
								tc["name"] = "audit_tool"
							}
							if choice == "required" {
								tc["type"] = "any"
							}
							input["tool_choice"] = tc
						} else if choice == "named" {
							tc := map[string]any{"type": "function", "name": "audit_tool"}
							if route == "/v1/chat/completions" {
								delete(tc, "name")
								tc["function"] = map[string]any{"name": "audit_tool"}
							}
							input["tool_choice"] = tc
						} else {
							input["tool_choice"] = choice
						}
					}
					if strings.Contains(control, "parallel") {
						parallel := control == "parallel-true-auto"
						if route == "/v1/messages" {
							tc, ok := input["tool_choice"].(map[string]any)
							if !ok {
								tc = map[string]any{"type": "auto"}
								input["tool_choice"] = tc
							}
							tc["disable_parallel_tool_use"] = !parallel
						} else {
							input["parallel_tool_calls"] = parallel
						}
					}
					if control == "strict-parallel-schema" {
						format := map[string]any{"type": "json_schema", "schema": schema}
						if route == "/v1/messages" {
							input["output_config"] = map[string]any{"format": format}
						} else if route == "/v1/responses" {
							format["name"] = "answer"
							format["strict"] = true
							input["text"] = map[string]any{"format": format}
						} else {
							input["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "answer", "strict": true, "schema": schema}}
						}
					}
					if strings.HasPrefix(control, "max") {
						effort := strings.Split(control, "-")[1]
						input[limitKey] = 64
						if strings.HasPrefix(control, "max1024") {
							input[limitKey] = 1024
						}
						if strings.HasPrefix(control, "max1025") {
							input[limitKey] = 1025
						}
						if strings.HasPrefix(control, "max4096") {
							input[limitKey] = 4096
						}
						if route == "/v1/messages" {
							input["output_config"] = map[string]any{"effort": effort}
						} else if route == "/v1/responses" {
							input["reasoning"] = map[string]any{"effort": effort}
						} else {
							input["reasoning_effort"] = effort
						}
					}
					body, err := json.Marshal(input)
					require.NoError(t, err)
					svc, up := newClaudeContractGateway(t)
					_, rec, callErr := callClaudeContract(t, svc, newClaude2292Account(kind), body, nil, route)
					fields := map[string]any{}
					for _, field := range []string{"model", "max_tokens", "thinking", "output_config", "tools", "tool_choice", "temperature", "top_p"} {
						fields[field] = gjson.GetBytes(up.body, field).Value()
					}
					row := map[string]any{"model": model, "account": kind, "route": route, "control": control, "input": input, "status": rec.Code, "calls": up.calls, "fields": fields}
					if callErr != nil {
						row["error"] = callErr.Error()
					}
					if up.request != nil {
						headers := map[string]string{}
						for key, values := range up.request.Header {
							require.Len(t, values, 1)
							headers[key] = values[0]
						}
						row["beta"] = getHeaderRaw(up.request.Header, "anthropic-beta")
						name := model + "-" + kind + strings.ReplaceAll(route, "/", "-") + "-" + control
						export := map[string]any{"method": up.request.Method, "path": up.request.URL.RequestURI(), "headers": headers, "raw_body_utf8": string(up.body), "wire_body_base64": base64.StdEncoding.EncodeToString(up.body), "upstream_profile": HTTPUpstreamProfileFromContext(up.request.Context()), "synthetic_auth": kind, "audit_case": name}
						data, err := json.MarshalIndent(export, "", "  ")
						require.NoError(t, err)
						require.NoError(t, os.WriteFile(filepath.Join(folder, "exports", name+".json"), append(data, '\n'), 0600))
					}
					rows = append(rows, row)
				}
			}
		}
	}
	data, err := json.MarshalIndent(rows, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(folder, "observations.json"), append(data, '\n'), 0600))
}
