//go:build unit

// Permanent contract for the same 540 inputs audited in CC-20261010-004.
package service

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClaudeContentContract(t *testing.T) {
	runClaudeContentContract(t, false)
}

func TestClaudeCallerContentContract(t *testing.T) {
	runClaudeContentContract(t, true)
}

func runClaudeContentContract(t *testing.T, preserveCaller bool) {
	inputPath, output := "testdata/claude_content_contract.json", os.Getenv("CLAUDE_CONTENT_CONTRACT_EXPORT")
	data, err := os.ReadFile(inputPath)
	require.NoError(t, err)
	var cases []struct {
		Name      string          `json:"name"`
		Messages  json.RawMessage `json:"messages"`
		Chat      json.RawMessage `json:"chat"`
		Responses json.RawMessage `json:"responses"`
		Expected  json.RawMessage `json:"expected"`
	}
	require.NoError(t, json.Unmarshal(data, &cases))
	if output != "" {
		require.NoError(t, os.MkdirAll(filepath.Join(output, "exports"), 0700))
	}
	var rows []map[string]any
	for _, model := range []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5-20251001", "claude-sonnet-5-5", "claude-opus-5-5", "claude-haiku-5-5"} {
		for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, route := range []string{"/v1/messages", "/v1/chat/completions", "/v1/responses"} {
				for _, tc := range cases {
					raw := tc.Messages
					if route == "/v1/chat/completions" {
						raw = tc.Chat
					}
					if route == "/v1/responses" {
						raw = tc.Responses
					}
					if string(raw) == "null" {
						continue
					}
					var body map[string]any
					require.NoError(t, json.Unmarshal(raw, &body))
					body["model"] = model
					payload, e := json.Marshal(body)
					require.NoError(t, e)
					svc, up := newClaudeContractGateway(t)
					svc.cfg.Gateway.ClaudeOAuthPreserveCaller = preserveCaller
					_, rec, callErr := callClaudeContract(t, svc, newClaude2292Account(kind), payload, nil, route)
					assertClaudeContentContract(t, model, kind, route, tc.Name, tc.Expected, rec.Code, up.calls, callErr, up.body, preserveCaller)
					row := map[string]any{"preserve_caller": preserveCaller, "model": model, "account": kind, "route": route, "control": tc.Name, "input": json.RawMessage(payload), "expected": tc.Expected, "status": rec.Code, "calls": up.calls}
					if callErr != nil {
						row["error"] = callErr.Error()
					}
					if up.request != nil {
						row["body"] = json.RawMessage(up.body)
						headers := map[string]string{}
						for key, values := range up.request.Header {
							require.Len(t, values, 1)
							headers[key] = values[0]
						}
						name := model + "-" + kind + strings.ReplaceAll(route, "/", "-") + "-" + tc.Name
						export := map[string]any{"method": up.request.Method, "path": up.request.URL.RequestURI(), "headers": headers, "raw_body_utf8": string(up.body), "wire_body_base64": base64.StdEncoding.EncodeToString(up.body), "upstream_profile": HTTPUpstreamProfileFromContext(up.request.Context()), "synthetic_auth": kind, "audit_case": name}
						b, e := json.MarshalIndent(export, "", "  ")
						require.NoError(t, e)
						if output != "" {
							require.NoError(t, os.WriteFile(filepath.Join(output, "exports", name+".json"), append(b, '\n'), 0600))
						}
					}
					rows = append(rows, row)
				}
			}
		}
	}
	b, e := json.MarshalIndent(rows, "", "  ")
	require.NoError(t, e)
	require.Len(t, rows, 540)
	if output != "" {
		require.NoError(t, os.WriteFile(filepath.Join(output, "observations.json"), append(b, '\n'), 0600))
	}
}

func contentContractObjects(value any) []map[string]any {
	var out []map[string]any
	switch v := value.(type) {
	case map[string]any:
		out = append(out, v)
		for _, child := range v {
			out = append(out, contentContractObjects(child)...)
		}
	case []any:
		for _, child := range v {
			out = append(out, contentContractObjects(child)...)
		}
	}
	return out
}
func contentContractContains(objects []map[string]any, expected map[string]any) bool {
	for _, object := range objects {
		same := true
		for k, v := range expected {
			if !reflect.DeepEqual(object[k], v) {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	return false
}
func assertClaudeContentContract(t *testing.T, model, kind, route, control string, expectJSON []byte, status, calls int, callErr error, body []byte, preserveCaller bool) {
	t.Helper()
	label := model + "/" + kind + route + "/" + control
	var expected map[string]any
	require.NoError(t, json.Unmarshal(expectJSON, &expected))
	reject := expected["reject_invalid_arguments"] == true || (model == "claude-sonnet-5-5" && expected["sampling_key"] != nil)
	if reject {
		require.Error(t, callErr, label)
		require.Equal(t, 400, status, label)
		require.Zero(t, calls, label)
		return
	}
	require.NoError(t, callErr, label)
	require.Equal(t, 200, status, label)
	require.Equal(t, 1, calls, label)
	var actual map[string]any
	require.NoError(t, json.Unmarshal(body, &actual), label)
	objects := contentContractObjects(actual["messages"])
	if text, ok := expected["system_text"].(string); ok {
		if kind == AccountTypeOAuth && !preserveCaller {
			require.True(t, contentContractContains(objects, map[string]any{"type": "text", "text": "[System Instructions]\n" + text}), label)
			require.True(t, contentContractContains(objects, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Understood. I will follow these instructions."}}}), label)
		} else {
			system := actual["system"]
			has := system == text
			if !has {
				has = contentContractContains(contentContractObjects(system), map[string]any{"type": "text", "text": text})
			}
			require.True(t, has, label)
		}
	}
	if typ, ok := expected["block_type"].(string); ok {
		media := objects
		if expected["tool_media"] == true && route != "/v1/chat/completions" {
			media = nil
			for _, object := range objects {
				if object["type"] == "tool_result" {
					media = append(media, contentContractObjects(object["content"])...)
				}
			}
		}
		require.True(t, contentContractContains(media, map[string]any{"type": typ, "source": expected["source"]}), label)
	}
	if strings.HasPrefix(control, "tool-") || control == "legacy-function" || control == "signed-thinking" || control == "redacted-thinking" {
		require.True(t, contentContractContains(objects, map[string]any{"type": "tool_use", "name": "audit_tool", "input": map[string]any{"value": "中文"}}), label)
		found := false
		for _, object := range objects {
			if object["type"] != "tool_result" {
				continue
			}
			content := object["content"]
			if control == "tool-empty" {
				found = content == "" || content == "(empty)"
			} else {
				found = content == "LOCAL_TOOL_RESULT" || contentContractContains(contentContractObjects(content), map[string]any{"text": "LOCAL_TOOL_RESULT"})
			}
			if found {
				break
			}
		}
		require.True(t, found, label)
	}
	if block, ok := expected["thinking_block"].(map[string]any); ok {
		require.True(t, contentContractContains(objects, block), label)
	}
	if key, ok := expected["sampling_key"].(string); ok {
		require.Equal(t, expected["sampling_value"], actual[key], label)
	}
}
