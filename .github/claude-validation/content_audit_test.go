//go:build unit

// Observation-only overlay. An independent checker evaluates every exported
// result; PASS here says the production calls ran, not that they were equal.
package service

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClaudeContentAudit(t *testing.T) {
	inputPath, output := os.Getenv("CLAUDE_CONTENT_FIXTURES"), os.Getenv("CLAUDE_CONTENT_OUTPUT")
	if inputPath == "" || output == "" {
		t.Skip("requires isolated content audit paths")
	}
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
	require.NoError(t, os.MkdirAll(filepath.Join(output, "exports"), 0700))
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
					_, rec, callErr := callClaudeContract(t, svc, newClaude2292Account(kind), payload, nil, route)
					row := map[string]any{"model": model, "account": kind, "route": route, "control": tc.Name, "input": json.RawMessage(payload), "expected": tc.Expected, "status": rec.Code, "calls": up.calls}
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
						require.NoError(t, os.WriteFile(filepath.Join(output, "exports", name+".json"), append(b, '\n'), 0600))
					}
					rows = append(rows, row)
				}
			}
		}
	}
	b, e := json.MarshalIndent(rows, "", "  ")
	require.NoError(t, e)
	require.NoError(t, os.WriteFile(filepath.Join(output, "observations.json"), append(b, '\n'), 0600))
}
