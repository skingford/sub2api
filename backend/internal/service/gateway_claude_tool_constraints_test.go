//go:build unit

// Permanent end-to-end contract for the 504 audited tool/reasoning combinations.
package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClaudeToolConstraintContract(t *testing.T) {
	folder := os.Getenv("CLAUDE_TOOL_CONSTRAINT_EXPORT")
	if folder != "" {
		require.NoError(t, os.MkdirAll(filepath.Join(folder, "exports"), 0700))
	}
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

					label := model + "/" + kind + route + "/" + control
					converted := route != "/v1/messages"
					signed := model == "claude-sonnet-5-5" || model == "claude-opus-5-5" || model == "claude-haiku-5-5"
					reject := (signed && (control == "named-tool" || control == "required-tool")) ||
						(converted && control == "max64-none" && (model == "claude-opus-5-5" || model == "claude-haiku-5-5")) ||
						(converted && model == "claude-haiku-4-5-20251001" && (control == "max64-high" || control == "max1024-high"))
					contract := map[string]any{"model": model, "max_tokens": input[limitKey]}
					if reject {
						require.Error(t, callErr, label)
						require.Equal(t, 400, rec.Code, label)
						require.Zero(t, up.calls, label)
					} else {
						require.NoError(t, callErr, label)
						require.Equal(t, 200, rec.Code, label)
						require.Equal(t, 1, up.calls, label)
						require.Equal(t, int64(input[limitKey].(int)), gjson.GetBytes(up.body, "max_tokens").Int(), label)
						if strings.Contains(control, "parallel") {
							serial := control != "parallel-true-auto"
							value := gjson.GetBytes(up.body, "tool_choice.disable_parallel_tool_use")
							require.True(t, value.Exists(), label)
							require.Equal(t, serial, value.Bool(), label)
							contract["disable_parallel_tool_use"] = serial
						}
						if strings.Contains(control, "strict") {
							strict := control != "strict-false"
							value := gjson.GetBytes(up.body, "tools.0.strict")
							require.True(t, value.Exists(), label)
							require.Equal(t, strict, value.Bool(), label)
							contract["strict"] = strict
							if strict {
								require.Contains(t, getHeaderRaw(up.request.Header, "anthropic-beta"), claude.BetaStructuredOutputsNative, label)
								expected, e := json.Marshal(schema)
								require.NoError(t, e)
								require.JSONEq(t, string(expected), gjson.GetBytes(up.body, "tools.0.input_schema").Raw, label)
								contract["tool_schema"] = schema
							}
						}
						if control == "strict-parallel-schema" {
							expected, e := json.Marshal(schema)
							require.NoError(t, e)
							require.JSONEq(t, string(expected), gjson.GetBytes(up.body, "output_config.format.schema").Raw, label)
							contract["output_schema"] = schema
						}
						if converted && !signed && control == "max64-none" {
							require.Equal(t, "disabled", gjson.GetBytes(up.body, "thinking.type").String(), label)
							require.False(t, gjson.GetBytes(up.body, "thinking.budget_tokens").Exists(), label)
							require.NotEqual(t, "none", gjson.GetBytes(up.body, "output_config.effort").String(), label)
							contract["thinking_type"] = "disabled"
						}
						if converted && !signed && strings.HasSuffix(control, "-high") {
							if model == "claude-haiku-4-5-20251001" {
								require.Equal(t, "enabled", gjson.GetBytes(up.body, "thinking.type").String(), label)
								expected := int64(1024)
								if control == "max4096-high" {
									expected = 4095
								}
								require.Equal(t, expected, gjson.GetBytes(up.body, "thinking.budget_tokens").Int(), label)
								contract["thinking_type"] = "enabled"
								contract["thinking_budget"] = expected
							} else {
								require.Equal(t, "adaptive", gjson.GetBytes(up.body, "thinking.type").String(), label)
								require.False(t, gjson.GetBytes(up.body, "thinking.budget_tokens").Exists(), label)
								contract["thinking_type"] = "adaptive"
							}
						}
					}
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
						export := map[string]any{"method": up.request.Method, "path": up.request.URL.RequestURI(), "headers": headers, "raw_body_utf8": string(up.body), "wire_body_base64": base64.StdEncoding.EncodeToString(up.body), "upstream_profile": HTTPUpstreamProfileFromContext(up.request.Context()), "synthetic_auth": kind, "audit_case": name, "contract": contract}
						data, err := json.MarshalIndent(export, "", "  ")
						require.NoError(t, err)
						if folder != "" {
							require.NoError(t, os.WriteFile(filepath.Join(folder, "exports", name+".json"), append(data, '\n'), 0600))
						}
					}
					rows = append(rows, row)
				}
			}
		}
	}
	require.Len(t, rows, 504)
	if folder == "" {
		return
	}
	data, err := json.MarshalIndent(rows, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(folder, "observations.json"), append(data, '\n'), 0600))
}

func TestClaudeStrictToolCannotLoseCapability(t *testing.T) {
	for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
		for _, mode := range []string{"drop", "override"} {
			for _, strict := range []string{"true", "false"} {
				svc, _ := newClaudeContractGateway(t)
				account := newClaude2292Account(kind)
				response := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(response)
				c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
				if mode == "drop" {
					c.Set(betaPolicyFilterSetKey, map[string]struct{}{claude.BetaStructuredOutputsNative: {}})
				} else {
					account.Credentials[credKeyHeaderOverrideEnabled] = true
					account.Credentials[credKeyHeaderOverrides] = map[string]any{"anthropic-beta": "oauth-2025-04-20"}
					require.NoError(t, NormalizeHeaderOverrideCredentials(account.Credentials))
				}
				body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"tools":[{"name":"lookup","strict":` + strict + `,"input_schema":{"type":"object","properties":{}}}]}`)
				var err error
				if kind == AccountTypeAPIKey {
					_, _, err = svc.buildUpstreamRequestAnthropicAPIKeyPassthrough(context.Background(), c, account, body, "synthetic")
				} else {
					_, _, err = svc.buildUpstreamRequest(context.Background(), c, account, body, "synthetic", "oauth", "claude-sonnet-4-6", false, true)
				}
				if strict == "false" || (mode == "override" && !account.IsHeaderOverrideEligible()) {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
					require.Equal(t, 400, response.Code)
				}
			}
		}
	}
}
