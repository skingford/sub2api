//go:build unit

package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
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

// This is the acceptance contract, not an observation-only audit. Every
// explicit constraint must survive all three routes and both account types.
func TestClaudeExplicitConstraintMatrix(t *testing.T) {
	models := []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5-20251001", "claude-sonnet-5-5", "claude-opus-5-5", "claude-haiku-5-5"}
	schema := map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}, "required": []string{"ok"}, "additionalProperties": false}
	expectedSchema, err := json.Marshal(schema)
	require.NoError(t, err)
	for _, model := range models {
		for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, route := range []string{"/v1/messages", "/v1/chat/completions", "/v1/responses"} {
				for _, control := range []string{"schema", "schema-effort", "max1", "max64", "stop-string", "stop-list"} {
					if strings.HasPrefix(control, "stop-") && route == "/v1/responses" {
						continue
					}
					t.Run(model+"/"+kind+route+"/"+control, func(t *testing.T) {
						input := map[string]any{"model": model}
						if route == "/v1/responses" {
							input["input"] = "LOCAL_CONSTRAINT_CONTRACT"
						} else {
							input["messages"] = []any{map[string]any{"role": "user", "content": "LOCAL_CONSTRAINT_CONTRACT"}}
						}
						limit := 4096
						if control == "max1" {
							limit = 1
						}
						if control == "max64" {
							limit = 64
						}
						key := "max_tokens"
						if route == "/v1/responses" {
							key = "max_output_tokens"
						}
						input[key] = limit
						if strings.HasPrefix(control, "schema") {
							if route == "/v1/messages" {
								input["output_config"] = map[string]any{"format": map[string]any{"type": "json_schema", "schema": schema}}
							} else if route == "/v1/responses" {
								input["text"] = map[string]any{"format": map[string]any{"type": "json_schema", "name": "answer", "strict": true, "schema": schema}}
							} else {
								input["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "answer", "strict": true, "schema": schema}}
							}
						}
						if control == "schema-effort" {
							if route == "/v1/messages" {
								input["output_config"].(map[string]any)["effort"] = "low"
							} else if route == "/v1/responses" {
								input["reasoning"] = map[string]any{"effort": "low"}
							} else {
								input["reasoning_effort"] = "low"
							}
						}
						stops := []string{"STOP", "停\n止"}
						if control == "stop-string" {
							stops = stops[:1]
						}
						if strings.HasPrefix(control, "stop-") {
							if route == "/v1/messages" {
								input["stop_sequences"] = stops
							} else if control == "stop-string" {
								input["stop"] = stops[0]
							} else {
								input["stop"] = stops
							}
						}
						body, err := json.Marshal(input)
						require.NoError(t, err)
						svc, up := newClaudeContractGateway(t)
						_, rec, err := callClaudeContract(t, svc, newClaude2292Account(kind), body, nil, route)
						require.NoError(t, err, rec.Body.String())
						require.Equal(t, 1, up.calls)
						require.Equal(t, int64(limit), gjson.GetBytes(up.body, "max_tokens").Int())
						if strings.HasPrefix(control, "schema") {
							require.JSONEq(t, string(expectedSchema), gjson.GetBytes(up.body, "output_config.format.schema").Raw)
							require.Contains(t, getHeaderRaw(up.request.Header, "anthropic-beta"), claude.BetaStructuredOutputsNative)
						}
						if control == "schema-effort" {
							require.Equal(t, "low", gjson.GetBytes(up.body, "output_config.effort").String())
						}
						if strings.HasPrefix(control, "stop-") {
							expected, err := json.Marshal(stops)
							require.NoError(t, err)
							require.JSONEq(t, string(expected), gjson.GetBytes(up.body, "stop_sequences").Raw)
						}
						if folder := os.Getenv("CLAUDE_CONSTRAINT_EXPORT"); folder != "" {
							require.NoError(t, os.MkdirAll(folder, 0700))
							headers := map[string]string{}
							for key, values := range up.request.Header {
								require.Len(t, values, 1)
								headers[key] = values[0]
							}
							contract := map[string]any{"max_tokens": limit, "model": model, "route": route, "control": control}
							if strings.HasPrefix(control, "schema") {
								contract["schema"] = schema
							}
							if strings.HasPrefix(control, "stop-") {
								contract["stop_sequences"] = stops
							}
							export := map[string]any{"method": up.request.Method, "path": up.request.URL.RequestURI(), "headers": headers, "raw_body_utf8": string(up.body), "wire_body_base64": base64.StdEncoding.EncodeToString(up.body), "upstream_profile": HTTPUpstreamProfileFromContext(up.request.Context()), "synthetic_auth": kind, "contract": contract}
							data, err := json.MarshalIndent(export, "", "  ")
							require.NoError(t, err)
							name := model + "-" + kind + strings.ReplaceAll(route, "/", "-") + "-" + control + ".json"
							require.NoError(t, os.WriteFile(filepath.Join(folder, name), append(data, '\n'), 0600))
						}
					})
				}
			}
		}
	}
}

func TestClaudeSchemaPolicyCannotSilentlyDropConstraint(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"schema"}],"output_config":{"format":{"type":"json_schema","schema":{"type":"object"}}}}`)
	for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
		for _, mode := range []string{"drop", "override"} {
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
			var err error
			var request *http.Request
			if kind == AccountTypeAPIKey {
				request, _, err = svc.buildUpstreamRequestAnthropicAPIKeyPassthrough(context.Background(), c, account, body, "synthetic")
			} else {
				request, _, err = svc.buildUpstreamRequest(context.Background(), c, account, body, "synthetic", "oauth", "claude-sonnet-4-6", false, true)
			}
			if mode == "override" && !account.IsHeaderOverrideEligible() {
				require.NoError(t, err)
				require.Contains(t, getHeaderRaw(request.Header, "anthropic-beta"), claude.BetaStructuredOutputsNative)
				continue
			}
			require.Error(t, err)
			require.Equal(t, http.StatusBadRequest, response.Code)
		}
	}
}

func TestClaudeConstraintErrorsReturn400BeforeDispatch(t *testing.T) {
	for _, model := range []string{"claude-sonnet-4-6", "claude-haiku-5-5"} {
		for route, fields := range map[string][]string{
			"/v1/chat/completions": {`"response_format":{"type":"json_object"}`, `"response_format":{"type":"json_schema","json_schema":{"name":"missing"}}`, `"stop":[null]`, `"max_tokens":0`},
			"/v1/responses":        {`"text":{"format":{"type":"json_object"}}`, `"text":{"format":{"type":"json_schema","schema":[]}}`, `"max_output_tokens":0`},
		} {
			for _, field := range fields {
				svc, up := newClaudeContractGateway(t)
				body := `{"model":"` + model + `",` + field + `,"messages":[{"role":"user","content":"hi"}],"input":"hi"}`
				_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), []byte(body), nil, route)
				require.Error(t, err)
				require.Equal(t, http.StatusBadRequest, rec.Code)
				require.Zero(t, up.calls)
			}
		}
	}
}

func TestClaudeConstraintMappingPrecedesOpenAIModelRules(t *testing.T) {
	svc, up := newClaudeContractGateway(t)
	a := newClaude2292Account(AccountTypeAPIKey)
	a.Credentials["model_mapping"] = map[string]any{"gpt-5-private-alias": "claude-haiku-5-5"}
	body := []byte(`{"model":"gpt-5-private-alias","messages":[{"role":"user","content":"hi"}],"max_tokens":64,"max_completion_tokens":7,"temperature":0.4,"stop":"STOP"}`)
	_, rec, err := callClaudeContract(t, svc, a, body, nil, "/v1/chat/completions")
	require.NoError(t, err, rec.Body.String())
	require.Equal(t, "claude-haiku-5-5", gjson.GetBytes(up.body, "model").String())
	require.Equal(t, int64(7), gjson.GetBytes(up.body, "max_tokens").Int())
	require.Equal(t, 0.4, gjson.GetBytes(up.body, "temperature").Float())
	require.Equal(t, "STOP", gjson.GetBytes(up.body, "stop_sequences.0").String())
}

func TestClaudeSummaryMatchesNativeUnicodeVectors(t *testing.T) {
	data, err := os.ReadFile("testdata/claude_summary_native_vectors.json")
	require.NoError(t, err)
	var cases []struct{ Name, Source, Expected string }
	require.NoError(t, json.Unmarshal(data, &cases))
	require.GreaterOrEqual(t, len(cases), 50)
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) { require.Equal(t, tc.Expected, recoveryCompactionSummary(tc.Source)) })
	}
}

func TestClaudeNestedSchemaCannotShadowBillingSystem(t *testing.T) {
	schema := `{"type":"object","properties":{"settings":{"type":"object","default":{"system":[],"model":"schema-value","max_tokens":7}}}}`
	for route, body := range map[string]string{
		"/v1/messages":         `{"model":"claude-haiku-5-5","messages":[{"role":"user","content":"hi"}],"output_config":{"format":{"type":"json_schema","schema":` + schema + `}}}`,
		"/v1/chat/completions": `{"model":"claude-haiku-5-5","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_schema","json_schema":{"name":"answer","schema":` + schema + `}}}`,
		"/v1/responses":        `{"model":"claude-haiku-5-5","input":"hi","text":{"format":{"type":"json_schema","schema":` + schema + `}}}`,
	} {
		t.Run(route, func(t *testing.T) {
			svc, up := newClaudeContractGateway(t)
			_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), []byte(body), nil, route)
			require.NoError(t, err, rec.Body.String())
			require.JSONEq(t, schema, gjson.GetBytes(up.body, "output_config.format.schema").Raw)
			require.True(t, strings.HasPrefix(string(up.body), `{"system":[`))
			require.NotContains(t, gjson.GetBytes(up.body, "system.0.text").String(), "cch=00000")
			if folder := os.Getenv("CLAUDE_NESTED_SCHEMA_EXPORT"); folder != "" {
				require.NoError(t, os.MkdirAll(folder, 0700))
				require.NoError(t, os.WriteFile(filepath.Join(folder, strings.ReplaceAll(route, "/", "-")+".json"), up.body, 0600))
			}
		})
	}
}
