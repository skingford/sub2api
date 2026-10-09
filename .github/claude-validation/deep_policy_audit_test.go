//go:build unit

package service

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Observations only: local mocks do not attest provider acceptance. Export the
// actual final request so independent native-runtime and wire checks can follow.
func TestDeepClaudePolicyBoundaries(t *testing.T) {
	folder := os.Getenv("CLAUDE_DEEP_POLICY_OUTPUT")
	if folder == "" {
		t.Skip("requires isolated audit output")
	}
	require.NoError(t, os.MkdirAll(filepath.Join(folder, "exports"), 0700))
	inputs := map[string]string{
		"runtime": "testdata/claude_code_2_1_292/parameter_alignment/native-gzip.json",
		"blocks":  "testdata/claude_code_2_1_292/gzip_alignment/gzip-blocks2-2.json",
		"count":   "testdata/claude_code_2_1_292/gzip_alignment/gzip-count-3.json",
	}
	var rows []map[string]any
	for input, path := range inputs {
		for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, passthrough := range []bool{false, true} {
				for _, policy := range []string{"baseline", "ua-newer", "ua-custom", "sdk-override", "version-override", "native-off", "transport-off", "model-map", "custom-origin"} {
					name := input + "-" + kind + "-" + map[bool]string{false: "normal", true: "passthrough"}[passthrough] + "-" + policy
					t.Run(name, func(t *testing.T) {
						c, body, wire := nativeGzipFixtureRequest(t, path)
						svc, _ := newClaudeContractGateway(t)
						up := &claudeGzipWireUpstream{}
						svc.httpUpstream = up
						a := newClaude2292Account(kind)
						a.Extra["anthropic_passthrough"] = passthrough
						overrides := map[string]any{}
						switch policy {
						case "ua-newer":
							overrides["user-agent"] = "claude-cli/2.1.293 (external, cli)"
						case "ua-custom":
							overrides["user-agent"] = "local-synthetic-client/1.0"
						case "sdk-override":
							overrides["x-stainless-package-version"] = "0.94.0"
						case "version-override":
							overrides["anthropic-version"] = "2023-01-01"
						case "native-off":
							a.Extra["claude_native_passthrough"] = false
						case "transport-off":
							a.Extra["claude_native_transport"] = false
						case "model-map":
							a.Credentials["model_mapping"] = map[string]any{"claude-sonnet-4-6": "claude-opus-4-6"}
						case "custom-origin":
							a.Credentials["base_url"] = "https://relay.example"
						}
						if len(overrides) > 0 {
							a.Credentials[credKeyHeaderOverrideEnabled] = true
							a.Credentials[credKeyHeaderOverrides] = overrides
							require.NoError(t, NormalizeHeaderOverrideCredentials(a.Credentials))
						}
						parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
						require.NoError(t, err)
						if input == "count" {
							err = svc.ForwardCountTokens(c.Request.Context(), c, a, parsed)
						} else {
							_, err = svc.Forward(c.Request.Context(), c, a, parsed)
						}
						row := map[string]any{"name": name, "input": input, "account": kind, "passthrough": passthrough, "policy": policy, "calls": up.calls, "status": c.Writer.Status()}
						if err != nil {
							row["error"] = err.Error()
						}
						if req := up.request; req != nil {
							logical := up.wire
							encoding := getHeaderRaw(req.Header, "Content-Encoding")
							if strings.EqualFold(encoding, "gzip") {
								r, e := gzip.NewReader(bytes.NewReader(up.wire))
								require.NoError(t, e)
								logical, e = io.ReadAll(r)
								require.NoError(t, e)
								require.NoError(t, r.Close())
							}
							row["encoding"] = encoding
							row["wire_preserved"] = bytes.Equal(wire, up.wire)
							row["logical_preserved"] = bytes.Equal(body, logical)
							row["profile"] = HTTPUpstreamProfileFromContext(req.Context())
							row["target"] = req.URL.String()
							row["user_agent"] = getHeaderRaw(req.Header, "User-Agent")
							row["application_encoding"] = claude.GzipUsesApplicationHeader2292(req.Context())
							row["billing"] = gjson.GetBytes(logical, "system.0.text").String()
							row["content_length_correct"] = req.ContentLength == int64(len(up.wire))
							if req.GetBody != nil {
								r, e := req.GetBody()
								require.NoError(t, e)
								b, e := io.ReadAll(r)
								require.NoError(t, e)
								require.NoError(t, r.Close())
								row["get_body_equal"] = bytes.Equal(b, up.wire)
							}
							if input != "count" {
								expected, e := finalizeClaude2292Billing(logical, gjson.GetBytes(logical, "system.0.text"))
								row["uncompressed_cch_equal"] = e == nil && bytes.Equal(expected, logical)
								if e != nil {
									row["cch_error"] = e.Error()
								}
							}
							headers := map[string]string{}
							for k, v := range req.Header {
								require.Len(t, v, 1)
								headers[k] = v[0]
							}
							export := map[string]any{"method": req.Method, "path": req.URL.RequestURI(), "headers": headers, "raw_body_utf8": string(logical), "wire_body_base64": base64.StdEncoding.EncodeToString(up.wire), "upstream_profile": HTTPUpstreamProfileFromContext(req.Context()), "synthetic_auth": kind, "target": req.URL.String(), "application_encoding": claude.GzipUsesApplicationHeader2292(req.Context())}
							b, e := json.MarshalIndent(export, "", "  ")
							require.NoError(t, e)
							require.NoError(t, os.WriteFile(filepath.Join(folder, "exports", name+".json"), append(b, '\n'), 0600))
						}
						rows = append(rows, row)
					})
				}
			}
		}
	}
	b, e := json.MarshalIndent(rows, "", "  ")
	require.NoError(t, e)
	require.NoError(t, os.WriteFile(filepath.Join(folder, "results.json"), append(b, '\n'), 0600))
}

// Keep retry-origin signals separate from payload/cipher comparisons. The
// exported response is replayed to the unmodified CLI in a different lab.
func TestDeepClaudeErrorOriginSignals(t *testing.T) {
	folder := os.Getenv("CLAUDE_DEEP_ERROR_OUTPUT")
	if folder == "" {
		t.Skip("requires isolated error replay output")
	}
	require.NoError(t, os.MkdirAll(folder, 0700))
	for _, marker := range []string{"cf-ray", "request-id", "none"} {
		t.Run(marker, func(t *testing.T) {
			svc, _ := newClaudeContractGateway(t)
			input, _, _ := nativeGzipRequest(t)
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = input.Request
			headers := http.Header{"Content-Type": []string{"application/json"}}
			if marker == "cf-ray" {
				headers.Set("cf-ray", "local-synthetic-ray-SJC")
			}
			if marker == "request-id" {
				headers.Set("request-id", "req_local_refusal")
			}
			original := []byte(`{"type":"error","error":{"type":"permission_error","message":"Local synthetic refusal"}}`)
			status := 403
			switch os.Getenv("CLAUDE_DEEP_ERROR_KIND") {
			case "bad-json-object":
				status = 400
				original = []byte(`{"error":"bad json: unexpected EOF","reason":"bad_json"}`)
			case "bad-json-text":
				status = 400
				headers.Set("Content-Type", "text/plain")
				original = []byte("bad json: unexpected EOF")
			case "bad-json-escaped":
				status = 400
				original = []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"The request body is not valid JSON ` + strings.Repeat("<", 2000) + `"}}`)
			case "bad-json-standard":
				status = 400
				original = []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"The request body is not valid JSON"}}`)
			}

			// This is the same production response finalizer used by the native retry
			// branch; no network response, classification or header implementation mocked.
			err := svc.returnClaudeUpstreamError(c.Request.Context(), c, newClaude2292Account(AccountTypeAPIKey), &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(bytes.NewReader(original))}, "claude-sonnet-4-6")
			require.Error(t, err)
			row := map[string]any{"upstream": map[string]any{"status": status, "headers": headers, "body": string(original)}, "downstream": map[string]any{"status": response.Code, "headers": response.Header(), "body": response.Body.String()}}
			b, e := json.MarshalIndent(row, "", "  ")
			require.NoError(t, e)
			require.NoError(t, os.WriteFile(filepath.Join(folder, marker+".json"), append(b, '\n'), 0600))
		})
	}
}
