//go:build unit

package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Compare native branches with explicit equivalent generic API controls.
// Differences are observations, not proof of acceptance by a real provider.
func TestComprehensiveGenericParameters(t *testing.T) {
	root, output := os.Getenv("CLAUDE_EXTENDED_AUDIT_INPUT"), os.Getenv("CLAUDE_GENERIC_AUDIT_OUTPUT")
	if root == "" || output == "" {
		t.Skip("requires isolated capture directory and output")
	}
	files, err := filepath.Glob(filepath.Join(root, "*", "case.json"))
	require.NoError(t, err)
	var results []map[string]any
	for _, file := range files {
		var scenario map[string]json.RawMessage
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(data, &scenario))
		controls, ok := scenario["generic_controls"]
		if !ok {
			continue
		}
		var captures []struct {
			Path    string            `json:"path"`
			Raw     string            `json:"raw_body_utf8"`
			Headers map[string]string `json:"headers"`
		}
		data, err = os.ReadFile(filepath.Join(filepath.Dir(file), "requests.json"))
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(data, &captures))
		for _, capture := range captures {
			if !strings.HasPrefix(capture.Path, "/v1/messages?") {
				continue
			}
			input := map[string]any{}
			require.NoError(t, json.Unmarshal(controls, &input))
			input["model"] = gjson.Get(capture.Raw, "model").String()
			input["messages"] = []any{map[string]any{"role": "user", "content": "LOCAL_COMPREHENSIVE_AUDIT"}}
			body, err := json.Marshal(input)
			require.NoError(t, err)
			svc, up := newClaudeContractGateway(t)
			_, response, forwardErr := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), body, nil, "/v1/messages")
			row := map[string]any{"case": filepath.Base(filepath.Dir(file)), "input": input, "status": response.Code, "calls": up.calls, "native_beta": capture.Headers["anthropic-beta"]}
			if forwardErr != nil {
				row["error"] = forwardErr.Error()
			}
			if up.request != nil {
				row["converted_beta"] = getHeaderRaw(up.request.Header, "anthropic-beta")
			}
			fields := map[string]any{}
			for _, key := range []string{"model", "max_tokens", "thinking", "temperature", "output_config", "context_management", "tool_choice"} {
				fields[key] = map[string]any{"native": gjson.Get(capture.Raw, key).Value(), "converted": gjson.GetBytes(up.body, key).Value()}
			}
			row["fields"] = fields
			row["converted_body"] = string(up.body)
			results = append(results, row)
			break
		}
	}
	require.NotEmpty(t, results)
	data, err := json.MarshalIndent(results, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(output, append(data, '\n'), 0600))
}
