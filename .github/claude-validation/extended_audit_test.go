//go:build unit

// Inject into internal/service with go -overlay. The production implementations
// are unchanged; only persistence and HTTP boundaries use existing test ports.
package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestExtendedNativeCLIAudit(t *testing.T) {
	root := os.Getenv("CLAUDE_EXTENDED_AUDIT_INPUT")
	if root == "" {
		t.Skip("requires isolated native capture directory")
	}
	gin.SetMode(gin.TestMode)
	var results []map[string]any
	for _, name := range []string{"parallel-read", "image-read", "thinking-tool", "compact", "resume", "alias-sonnet", "extra-metadata", "structured-output"} {
		t.Run(name, func(t *testing.T) {
			var records []struct {
				Method  string            `json:"method"`
				Path    string            `json:"path"`
				Headers map[string]string `json:"headers"`
				Raw     string            `json:"raw_body_utf8"`
			}
			data, err := os.ReadFile(filepath.Join(root, name, "requests.json"))
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(data, &records))
			var responses []struct {
				Index int    `json:"request_index"`
				Body  string `json:"body"`
			}
			data, err = os.ReadFile(filepath.Join(root, name, "responses.json"))
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(data, &responses))
			responseByIndex := map[int]string{}
			for _, r := range responses {
				responseByIndex[r.Index] = r.Body
			}
			var previous *RecoveryHistory
			store := &recoveryLabStore{rows: map[RecoveryScope]RecoveryRow{}, sent: map[string]bool{}}
			manager := NewClaudeRecoveryService(config.ClaudeRecoveryConfig{Enabled: true, GroupIDs: []int64{7}, EncryptionKey: base64.StdEncoding.EncodeToString(make([]byte, 32)), SummaryAPIKey: "fake-summary-only", SummaryModel: "fake-model"}.WithDefaults(), store, recoveryLabSummary{})
			manager.Close() // Disable the background summary worker in this request audit.
			svc, _ := newClaudeContractGateway(t)
			account := newClaude2292Account(AccountTypeAPIKey)
			account.GroupIDs = []int64{7}
			account.AccountGroups = []AccountGroup{{GroupID: 7}}
			svc.accountRepo = &mockAccountRepoForPlatform{accountsByID: map[int64]*Account{account.ID: account}}
			for i, r := range records {
				if !strings.HasPrefix(r.Path, "/v1/messages") {
					continue
				}
				body := []byte(r.Raw)
				for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
					for _, passthrough := range []bool{false, true} {
						svc, up := newClaudeContractGateway(t)
						account := newClaude2292Account(kind)
						account.Extra["anthropic_passthrough"] = passthrough
						_, rec, e := callClaudeContract(t, svc, account, body, r.Headers, r.Path)
						item := map[string]any{"case": name, "index": i, "account": kind, "passthrough": passthrough,
							"status": rec.Code, "calls": up.calls, "body_equal": bytes.Equal(body, up.body),
							"input_sha256": recoveryHash(body), "output_sha256": recoveryHash(up.body)}
						if e != nil {
							item["error"] = e.Error()
						}
						if up.request != nil {
							diffs := []string{}
							seen := map[string]bool{}
							ignore := func(k string) bool {
								switch k {
								case "authorization", "x-api-key", "host", "connection", "content-length":
									return true
								}
								return false
							}
							for key, value := range r.Headers {
								lower := strings.ToLower(key)
								seen[lower] = true
								if ignore(lower) {
									continue
								}
								got := getHeaderRaw(up.request.Header, key)
								if got != value {
									diffs = append(diffs, key+": "+value+" -> "+got)
								}
							}
							for key := range up.request.Header {
								lower := strings.ToLower(key)
								if !seen[lower] && !ignore(lower) {
									diffs = append(diffs, "added "+key+": "+getHeaderRaw(up.request.Header, key))
								}
							}
							sort.Strings(diffs)
							item["header_differences"] = diffs
							item["profile"] = HTTPUpstreamProfileFromContext(up.request.Context())
						}
						results = append(results, item)
					}
				}
				h, e := parseRecoveryHistory(body, "messages", 8*1024*1024)
				item := map[string]any{"case": name, "index": i, "kind": "managed_history"}
				if e != nil {
					item["parse_error"] = e.Error()
				} else {
					item["closed_tools"] = recoveryClosedTools(h)
					item["safe_migration_boundary"] = recoverySafeBoundary(h)
					if previous != nil {
						item["extends_previous"] = recoveryHistoryExtends(*previous, h)
						item["config_equal"] = previous.ConfigHash == h.ConfigHash
						item["previous_messages"] = len(previous.Messages)
						item["next_messages"] = len(h.Messages)
						var mismatches []int
						for j := 0; j < len(previous.Messages) && j < len(h.Messages); j++ {
							if recoveryMessageHash(previous.Messages[j], "messages") != recoveryMessageHash(h.Messages[j], "messages") {
								mismatches = append(mismatches, j)
							}
						}
						item["mismatched_message_indices"] = mismatches
					}
					if response, ok := responseByIndex[i]; ok {
						completed, err := recoveryCompletedHistory(h, []byte(response), true)
						if err != nil {
							item["response_error"] = err.Error()
						} else {
							previous = &completed
						}
					}
				}
				results = append(results, item)
				uid := ParseMetadataUserID(gjson.GetBytes(body, "metadata.user_id").String())
				require.NotNil(t, uid)
				flow := map[string]any{"case": name, "index": i, "kind": "managed_service"}
				p, beginErr := manager.Begin(context.Background(), svc, RecoveryScope{UserID: 1, GroupID: 7, ClientSession: uid.SessionID}, body, "messages", "", false)
				if beginErr != nil {
					flow["begin_error"] = beginErr.Error()
				} else {
					e := p.BeforeSend(context.Background(), account)
					if e != nil {
						flow["before_send_error"] = e.Error()
					} else {
						p.ResponseStatus.Store(200)
					}
					finishErr := p.Finish(context.Background(), 200, nil, []byte(responseByIndex[i]), e == nil, "")
					if finishErr != nil {
						flow["finish_error"] = finishErr.Error()
					}
					flow["sent"] = p.Sent.Load()
				}
				results = append(results, flow)
			}
		})
	}
	data, err := json.MarshalIndent(results, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(os.Getenv("CLAUDE_EXTENDED_AUDIT_OUTPUT"), append(data, '\n'), 0600))
}

func TestExtendedNativeGenericProfiles(t *testing.T) {
	root := os.Getenv("CLAUDE_EXTENDED_AUDIT_INPUT")
	if root == "" {
		t.Skip("requires isolated native capture directory")
	}
	var results []map[string]any
	for _, name := range []string{"alias-sonnet", "parallel-read"} {
		data, err := os.ReadFile(filepath.Join(root, name, "requests.json"))
		require.NoError(t, err)
		var records []struct {
			Path    string            `json:"path"`
			Raw     string            `json:"raw_body_utf8"`
			Headers map[string]string `json:"headers"`
		}
		require.NoError(t, json.Unmarshal(data, &records))
		for _, r := range records {
			if !strings.HasPrefix(r.Path, "/v1/messages") {
				continue
			}
			model := gjson.Get(r.Raw, "model").String()
			input, err := json.Marshal(map[string]any{"model": model, "messages": []any{map[string]any{"role": "user", "content": "LOCAL_EXTENDED_AUDIT inspect synthetic fixtures and reply OK."}}})
			require.NoError(t, err)
			svc, up := newClaudeContractGateway(t)
			_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), input, nil, "/v1/messages")
			require.NoError(t, err)
			item := map[string]any{"model": model, "status": rec.Code, "reference_mode": "API key stream-json", "conversion_mode": "generic API to OAuth", "native_beta": r.Headers["anthropic-beta"], "converted_beta": getHeaderRaw(up.request.Header, "anthropic-beta")}
			for _, field := range []string{"max_tokens", "temperature", "thinking", "output_config", "context_management"} {
				item[field] = map[string]any{"native": gjson.Get(r.Raw, field).Value(), "converted": gjson.GetBytes(up.body, field).Value()}
			}
			results = append(results, item)
			break
		}
	}
	data, err := json.MarshalIndent(results, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(os.Getenv("CLAUDE_EXTENDED_AUDIT_OUTPUT")+".profiles.json", append(data, '\n'), 0600))
}

func TestExtendedNativeExplicitDisplay(t *testing.T) {
	output := os.Getenv("CLAUDE_EXTENDED_AUDIT_OUTPUT")
	if output == "" {
		t.Skip("requires audit output path")
	}
	var results []map[string]any
	for _, explicitBeta := range []bool{false, true} {
		svc, up := newClaudeContractGateway(t)
		headers := map[string]string{}
		if explicitBeta {
			headers["anthropic-beta"] = "thinking-display-updates-2026-08-18"
		}
		body := []byte(`{"model":"claude-sonnet-4-6","thinking":{"type":"adaptive","display":"updates"},"messages":[{"role":"user","content":"LOCAL_EXTENDED_AUDIT"}]}`)
		_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), body, headers, "/v1/messages")
		require.NoError(t, err)
		results = append(results, map[string]any{"explicit_beta": explicitBeta, "status": rec.Code, "thinking": gjson.GetBytes(up.body, "thinking").Value(), "beta": getHeaderRaw(up.request.Header, "anthropic-beta")})
	}
	data, err := json.MarshalIndent(results, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(output+".display.json", append(data, '\n'), 0600))
}
