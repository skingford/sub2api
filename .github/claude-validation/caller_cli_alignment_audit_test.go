//go:build unit

// Observation overlay: these assertions check execution, not CLI equivalence.
package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCallerCLIAlignmentObserve(t *testing.T) {
	input, output := os.Getenv("CLAUDE_ALIGNMENT_INPUT"), os.Getenv("CLAUDE_ALIGNMENT_OUTPUT")
	if input == "" || output == "" {
		t.Skip("requires isolated fixtures and output")
	}
	data, err := os.ReadFile(input)
	require.NoError(t, err)
	var cases []struct {
		Name    string            `json:"name"`
		Version string            `json:"version"`
		Native  bool              `json:"native"`
		Route   string            `json:"route"`
		Body    json.RawMessage   `json:"body"`
		Headers map[string]string `json:"headers"`
	}
	require.NoError(t, json.Unmarshal(data, &cases))
	require.NoError(t, os.MkdirAll(filepath.Join(output, "exports"), 0700))
	defer claude.SetCLIVersionResolver(nil)
	var rows []map[string]any
	for _, tc := range cases {
		claude.SetCLIVersionResolver(func() string { return tc.Version })
		for _, preserve := range []bool{false, true} {
			svc, up := newClaudeContractGateway(t)
			svc.cfg.Gateway.ClaudeOAuthPreserveCaller = preserve
			account := newClaude2292Account(AccountTypeOAuth)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", tc.Route, bytes.NewReader(tc.Body))
			for key, value := range tc.Headers {
				c.Request.Header.Set(key, value)
			}
			c.Request.Header.Set("X-Sub2api-Trace-ID", "local-audit-canary")
			c.Request.Header.Set("X-Sub2API-Recovery-Purpose", "local-audit-canary")
			ctx := context.Background()
			if tc.Native {
				var body map[string]any
				require.NoError(t, json.Unmarshal(tc.Body, &body))
				valid := NewClaudeCodeValidator().Validate(c.Request, body)
				ctx = SetClaudeCodeClient(ctx, valid)
				c.Request = c.Request.WithContext(ctx)
			}
			var callErr error
			switch {
			case strings.Contains(tc.Route, "chat/completions"):
				_, callErr = svc.ForwardAsChatCompletions(ctx, c, account, tc.Body, nil)
			case strings.Contains(tc.Route, "responses"):
				_, callErr = svc.ForwardAsResponses(ctx, c, account, tc.Body, nil)
			default:
				parsed, e := ParseGatewayRequest(NewRequestBodyRef(tc.Body), PlatformAnthropic)
				require.NoError(t, e)
				if strings.Contains(tc.Route, "count_tokens") {
					callErr = svc.ForwardCountTokens(ctx, c, account, parsed)
				} else {
					_, callErr = svc.Forward(ctx, c, account, parsed)
				}
			}
			row := map[string]any{"name": tc.Name, "version": tc.Version, "native": tc.Native, "route": tc.Route, "preserve_caller": preserve, "calls": up.calls, "status": rec.Code}
			if callErr != nil {
				row["error"] = callErr.Error()
			}
			if up.request != nil {
				body := up.body
				if strings.EqualFold(getHeaderRaw(up.request.Header, "Content-Encoding"), "gzip") {
					reader, e := gzip.NewReader(bytes.NewReader(body))
					require.NoError(t, e)
					body, e = io.ReadAll(reader)
					require.NoError(t, e)
					require.NoError(t, reader.Close())
				}
				row["body"] = json.RawMessage(body)
				headers := map[string]string{}
				for key, values := range up.request.Header {
					headers[key] = strings.Join(values, ", ")
				}
				row["headers"] = headers
				row["profile"] = HTTPUpstreamProfileFromContext(up.request.Context())
				name := tc.Name + "-legacy"
				if preserve {
					name = tc.Name + "-preserve"
				}
				export := map[string]any{"method": "POST", "path": up.request.URL.RequestURI(), "headers": headers, "raw_body_utf8": string(body), "wire_body_base64": base64.StdEncoding.EncodeToString(up.body), "upstream_profile": HTTPUpstreamProfileFromContext(up.request.Context()), "synthetic_auth": "oauth", "audit_case": name}
				encoded, e := json.MarshalIndent(export, "", "  ")
				require.NoError(t, e)
				require.NoError(t, os.WriteFile(filepath.Join(output, "exports", name+".json"), encoded, 0600))
			}
			rows = append(rows, row)
		}
	}
	encoded, err := json.MarshalIndent(rows, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(output, "observations.json"), encoded, 0600))
}
