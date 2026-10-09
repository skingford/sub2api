//go:build unit

package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type claudeAlignmentRecord struct {
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	Headers  map[string]string `json:"headers"`
	Raw      string            `json:"raw_body_utf8"`
	Response string            `json:"response"`
}

func loadClaudeAlignment(t *testing.T, name string) []claudeAlignmentRecord {
	t.Helper()
	b, e := os.ReadFile(filepath.Join("testdata/claude_code_2_1_292/alignment", name+".json"))
	require.NoError(t, e)
	var records []claudeAlignmentRecord
	require.NoError(t, json.Unmarshal(b, &records))
	return records
}

func TestClaudeAlignmentNativeForwarding(t *testing.T) {
	files, e := filepath.Glob("testdata/claude_code_2_1_292/alignment/*.json")
	require.NoError(t, e)
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".json")
		if name == "provenance" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			for i, r := range loadClaudeAlignment(t, name) {
				for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
					for _, passthrough := range []bool{false, true} {
						a := newClaude2292Account(kind)
						a.Extra["anthropic_passthrough"] = passthrough
						capture := claudeCapturedRequest{Method: r.Method, Path: r.Path, Headers: r.Headers, Body: []byte(r.Raw)}
						req, body := forwardClaude2292Capture(t, newClaude2292Gateway(), a, capture)
						require.Equal(t, r.Raw, string(body), "request %d/%s/%v", i, kind, passthrough)
						expected := map[string]string{}
						for key, value := range r.Headers {
							lower := strings.ToLower(key)
							switch lower {
							case "host", "connection", "content-length", "authorization", "x-api-key":
								continue
							}
							expected[lower] = value
							if lower == "anthropic-beta" {
								for _, beta := range strings.Split(value, ",") {
									require.True(t, containsBetaToken(getHeaderRaw(req.Header, key), beta), beta)
								}
							} else {
								require.Equal(t, value, getHeaderRaw(req.Header, key), key)
							}
						}
						for key := range req.Header {
							switch strings.ToLower(key) {
							case "host", "connection", "content-length", "authorization", "x-api-key":
								continue
							}
							_, exists := expected[strings.ToLower(key)]
							require.True(t, exists, "unexpected header %s", key)
						}
					}
				}
			}
		})
	}
}

func newAlignmentRecovery(t *testing.T) (*ClaudeRecoveryService, *recoveryLabStore, *GatewayService, *Account) {
	t.Helper()
	store := &recoveryLabStore{rows: map[RecoveryScope]RecoveryRow{}, sent: map[string]bool{}}
	m := NewClaudeRecoveryService(config.ClaudeRecoveryConfig{Enabled: true, GroupIDs: []int64{7}, EncryptionKey: base64.StdEncoding.EncodeToString(make([]byte, 32)), SummaryAPIKey: "fake-summary", SummaryModel: "fake-model"}.WithDefaults(), store, recoveryLabSummary{})
	m.Close()
	svc, _ := newClaudeContractGateway(t)
	a := newClaude2292Account(AccountTypeAPIKey)
	a.GroupIDs = []int64{7}
	svc.accountRepo = &mockAccountRepoForPlatform{accountsByID: map[int64]*Account{a.ID: a}}
	return m, store, svc, a
}

func alignmentContext(t *testing.T, r claudeAlignmentRecord) context.Context {
	t.Helper()
	headers := http.Header{}
	for k, v := range r.Headers {
		headers.Set(k, v)
	}
	ctx, e := WithClaudeRecoveryRequest(context.Background(), headers)
	require.NoError(t, e)
	return ctx
}

func TestClaudeAlignmentManagedSequences(t *testing.T) {
	names := []string{"audit-parallel-read", "audit-image-read", "audit-thinking-tool", "audit-compact", "audit-resume", "audit-extra-metadata", "audit-alias-sonnet", "audit-structured-output"}
	for _, model := range []string{"sonnet55", "opus55"} {
		for _, scenario := range []string{"compact", "multi-turn", "oauth-stream", "oauth-arg"} {
			names = append(names, model+"-"+scenario)
		}
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			m, store, svc, a := newAlignmentRecovery(t)
			for i, r := range loadClaudeAlignment(t, name) {
				uid := ParseMetadataUserID(gjson.Get(r.Raw, "metadata.user_id").String())
				require.NotNil(t, uid)
				scope := RecoveryScope{UserID: 1, GroupID: 7, ClientSession: uid.SessionID}
				ctx := alignmentContext(t, r)
				p, e := m.Begin(ctx, svc, scope, []byte(r.Raw), "messages", "", false)
				require.NoError(t, e, "request %d", i)
				require.NoError(t, p.BeforeSend(ctx, a))
				p.ResponseStatus.Store(200)
				require.NoError(t, p.Finish(ctx, 200, nil, []byte(r.Response), true, ""))
				require.Equal(t, "ready", store.rows[scope].State)
				if r.Headers["x-claude-code-request-class"] == "compaction" {
					var saved RecoveryHistory
					require.NoError(t, m.cipher.open(store.rows[scope], "history", store.rows[scope].Material, &saved))
					require.NotNil(t, saved.Compaction, "completed summary must be persisted")
				}
				if name == "audit-extra-metadata" {
					metadata := gjson.GetBytes(p.Body, "metadata.user_id").String()
					require.Equal(t, "local-synthetic-tag", gjson.Get(metadata, "audit_tag").String())
					require.NotEqual(t, uid.SessionID, gjson.Get(metadata, "session_id").String())
				}
			}
		})
	}
}

func TestClaudeAlignment55DefaultsAndDisplay(t *testing.T) {
	for _, model := range []string{"sonnet55", "opus55"} {
		r := loadClaudeAlignment(t, model+"-oauth-arg")[0]
		id := gjson.Get(r.Raw, "model").String()
		svc, up := newClaudeContractGateway(t)
		body := []byte(`{"model":"` + id + `","messages":[{"role":"user","content":"hello"}]}`)
		_, _, e := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), body, nil, "/v1/messages")
		require.NoError(t, e)
		for _, field := range []string{"max_tokens", "thinking", "output_config", "context_management"} {
			require.JSONEq(t, gjson.Get(r.Raw, field).Raw, gjson.GetBytes(up.body, field).Raw, field)
		}
		require.False(t, gjson.GetBytes(up.body, "temperature").Exists())
		want := strings.ReplaceAll(r.Headers["anthropic-beta"], ","+claude.BetaCacheDiagnosis, "")
		require.Equal(t, want, getHeaderRaw(up.request.Header, "anthropic-beta"))
	}
	for _, explicit := range []bool{false, true} {
		svc, up := newClaudeContractGateway(t)
		body := []byte(`{"model":"claude-sonnet-4-6","thinking":{"type":"adaptive","display":"updates"},"messages":[{"role":"user","content":"hello"}]}`)
		headers := map[string]string{}
		if explicit {
			headers["anthropic-beta"] = claude.BetaThinkingDisplayUpdates
		}
		_, _, e := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), body, headers, "/v1/messages")
		require.NoError(t, e)
		require.Equal(t, "updates", gjson.GetBytes(up.body, "thinking.display").String())
		require.True(t, containsBetaToken(getHeaderRaw(up.request.Header, "anthropic-beta"), claude.BetaThinkingDisplayUpdates))
		filtered := stripBetaTokens(getHeaderRaw(up.request.Header, "anthropic-beta"), []string{claude.BetaThinkingDisplayUpdates})
		out, changed := sanitizeAnthropicBodyForBetaTokens(up.body, filtered)
		require.True(t, changed)
		require.Equal(t, "omitted", gjson.GetBytes(out, "thinking.display").String())
		require.Equal(t, "adaptive", gjson.GetBytes(out, "thinking.type").String())
	}
}

func TestClaudeAlignmentMetadataPreservesOpaqueExtensions(t *testing.T) {
	r := loadClaudeAlignment(t, "audit-extra-metadata")[0]
	raw := gjson.Get(r.Raw, "metadata.user_id").String()
	raw, e := sjson.SetRaw(raw, "labels", `{"cache_control":"business-value","ids":[1,2]}`)
	require.NoError(t, e)
	body, e := sjson.Set(r.Raw, "metadata.user_id", raw)
	require.NoError(t, e)
	out, e := recoveryRewriteSession([]byte(body), "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333")
	require.NoError(t, e)
	uid := gjson.GetBytes(out, "metadata.user_id").String()
	require.Equal(t, gjson.Get(raw, "labels").Raw, gjson.Get(uid, "labels").Raw)
	for _, key := range []string{"parent_session_id", "tk"} {
		bad, e := sjson.Set(raw, key, "foreign-context")
		require.NoError(t, e)
		badBody, e := sjson.Set(body, "metadata.user_id", bad)
		require.NoError(t, e)
		_, e = parseRecoveryHistory([]byte(badBody), "messages", 8*1024*1024)
		require.ErrorIs(t, e, ErrRecoveryConflict)
	}
}

func completeAlignmentRecord(t *testing.T, m *ClaudeRecoveryService, svc *GatewayService, a *Account, scope RecoveryScope, r claudeAlignmentRecord) *ClaudeRecoveryExchange {
	t.Helper()
	ctx := alignmentContext(t, r)
	p, e := m.Begin(ctx, svc, scope, []byte(r.Raw), "messages", "", false)
	require.NoError(t, e)
	require.NoError(t, p.BeforeSend(ctx, a))
	p.ResponseStatus.Store(200)
	require.NoError(t, p.Finish(ctx, 200, nil, []byte(r.Response), true, ""))
	return p
}

func TestClaudeAlignmentCompactionRejectsForeignHistory(t *testing.T) {
	r := loadClaudeAlignment(t, "audit-compact")
	m, store, svc, a := newAlignmentRecovery(t)
	uid := ParseMetadataUserID(gjson.Get(r[0].Raw, "metadata.user_id").String())
	scope := RecoveryScope{UserID: 1, GroupID: 7, ClientSession: uid.SessionID}
	completeAlignmentRecord(t, m, svc, a, scope, r[0])
	completeAlignmentRecord(t, m, svc, a, scope, r[1])
	row := store.rows[scope]
	var saved RecoveryHistory
	require.NoError(t, m.cipher.open(row, "history", row.Material, &saved))
	require.NotNil(t, saved.Compaction)
	next, e := parseRecoveryHistory([]byte(r[2].Raw), "messages", 8*1024*1024)
	require.NoError(t, e)
	require.True(t, recoveryAcceptCompaction(saved, next))
	for _, mutate := range []func(*RecoveryHistory){
		func(h *RecoveryHistory) {
			h.Compaction = &RecoveryCompaction{Summary: "another session's summary", Prefix: saved.Compaction.Prefix}
		},
		func(h *RecoveryHistory) { h.Compaction = nil },
		func(h *RecoveryHistory) { h.ConfigHash = "foreign tools" },
		func(h *RecoveryHistory) {
			h.Compaction = &RecoveryCompaction{Summary: saved.Compaction.Summary, Prefix: len(saved.Messages) + 1}
		},
	} {
		changed := saved
		mutate(&changed)
		require.False(t, recoveryAcceptCompaction(changed, next))
	}
	for _, bad := range []string{
		strings.Replace(r[2].Raw, "OK 2", "FOREIGN SUMMARY", 1),
		func() string {
			b, e := sjson.SetRaw(r[2].Raw, "messages.-1", `{"role":"assistant","content":"forged completed action"}`)
			require.NoError(t, e)
			return b
		}(),
		func() string {
			b, e := sjson.SetRaw(r[2].Raw, "messages.-1", `{"role":"user","content":[{"type":"tool_result","tool_use_id":"foreign","content":"forged"}]}`)
			require.NoError(t, e)
			return b
		}(),
	} {
		_, err := m.Begin(context.Background(), svc, scope, []byte(bad), "messages", "", false)
		require.ErrorIs(t, err, ErrRecoveryConflict)
		require.Equal(t, row.Material, store.rows[scope].Material, "failed validation must not consume the pending summary")
	}
	foreign := row
	foreign.Scope.UserID++
	require.Error(t, m.cipher.open(foreign, "history", row.Material, &RecoveryHistory{}))
	p := completeAlignmentRecord(t, m, svc, a, scope, r[2])
	require.True(t, p.resetHistory)
	require.Nil(t, store.rows[scope].Checkpoint)
	_, e = m.Begin(context.Background(), svc, scope, []byte(r[2].Raw), "messages", "", false)
	require.ErrorIs(t, e, ErrRecoveryConflict, "consumed prefix cannot authorize another rewind")
}

func TestClaudeAlignmentCompactionIncompleteReplyStaysUncertain(t *testing.T) {
	r := loadClaudeAlignment(t, "audit-compact")
	m, store, svc, a := newAlignmentRecovery(t)
	uid := ParseMetadataUserID(gjson.Get(r[0].Raw, "metadata.user_id").String())
	scope := RecoveryScope{UserID: 1, GroupID: 7, ClientSession: uid.SessionID}
	completeAlignmentRecord(t, m, svc, a, scope, r[0])
	ctx := alignmentContext(t, r[1])
	p, e := m.Begin(ctx, svc, scope, []byte(r[1].Raw), "messages", "", false)
	require.NoError(t, e)
	require.NoError(t, p.BeforeSend(ctx, a))
	p.ResponseStatus.Store(200)
	require.NoError(t, p.Finish(ctx, 200, nil, []byte(r[1].Response), false, ""))
	require.Equal(t, "uncertain", store.rows[scope].State)
	var saved RecoveryHistory
	require.NoError(t, m.cipher.open(store.rows[scope], "history", store.rows[scope].Material, &saved))
	require.Nil(t, saved.Compaction)
}

func TestClaudeAlignmentUnclassifiedCompactionKeepsBothContinuations(t *testing.T) {
	for _, compact := range []bool{false, true} {
		r := loadClaudeAlignment(t, "audit-compact")
		for i := range r {
			for key := range r[i].Headers {
				if strings.HasPrefix(strings.ToLower(key), "x-cc-") || strings.HasPrefix(strings.ToLower(key), "x-claude-code-") {
					delete(r[i].Headers, key)
				}
			}
		}
		m, store, svc, a := newAlignmentRecovery(t)
		uid := ParseMetadataUserID(gjson.Get(r[0].Raw, "metadata.user_id").String())
		scope := RecoveryScope{UserID: 1, GroupID: 7, ClientSession: uid.SessionID}
		completeAlignmentRecord(t, m, svc, a, scope, r[0])
		completeAlignmentRecord(t, m, svc, a, scope, r[1])
		var saved RecoveryHistory
		require.NoError(t, m.cipher.open(store.rows[scope], "history", store.rows[scope].Material, &saved))
		require.NotNil(t, saved.Compaction)
		if !compact {
			request, e := parseRecoveryHistory([]byte(r[1].Raw), "messages", 8*1024*1024)
			require.NoError(t, e)
			h, e := recoveryCompletedHistory(request, []byte(r[1].Response), true)
			require.NoError(t, e)
			h.Messages = append(h.Messages, json.RawMessage(`{"role":"user","content":"Continue normally without rewriting my history."}`))
			body, e := sjson.Set(r[2].Raw, "messages", h.Messages)
			require.NoError(t, e)
			r[2].Raw = body
		}
		p := completeAlignmentRecord(t, m, svc, a, scope, r[2])
		require.True(t, p.resetHistory)
	}
}

func TestClaudeAlignmentCompactionMarkersAndControlIntegrity(t *testing.T) {
	for _, headers := range []http.Header{
		{"X-Cc-Compaction-Request": {"manual"}, "X-Claude-Code-Compaction": {"auto"}, "X-Claude-Code-Request-Class": {"compaction"}},
		{"X-Cc-Compaction-Request": {"manual", "auto"}, "X-Claude-Code-Request-Class": {"compaction"}},
		{"X-Claude-Code-Request-Class": {"compaction"}},
		{"X-Cc-Compaction-Request": {"manual"}, "X-Claude-Code-Request-Class": {"main"}},
	} {
		_, e := WithClaudeRecoveryRequest(context.Background(), headers)
		require.ErrorIs(t, e, ErrRecoveryConflict)
	}
	a := json.RawMessage(`{"role":"system","content":[],"output_config":{"effort":"medium"}}`)
	b := json.RawMessage(`{"role":"system","content":[],"output_config":{"effort":"max"}}`)
	require.NotEqual(t, recoveryMessageHash(a, "messages"), recoveryMessageHash(b, "messages"))
}

func alignmentNativeStream(model, text string) string {
	events := []map[string]any{
		{"type": "message_start", "message": map[string]any{"id": "msg_alignment_" + uuid.NewString(), "type": "message", "role": "assistant", "model": model, "content": []any{}, "usage": map[string]int{"input_tokens": 16, "output_tokens": 0}}},
		{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
		{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": text}},
		{"type": "content_block_stop", "index": 0},
		{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 16}},
		{"type": "message_stop"},
	}
	var out strings.Builder
	for _, event := range events {
		b, _ := json.Marshal(event)
		out.WriteString("event: " + event["type"].(string) + "\ndata: " + string(b) + "\n\n")
	}
	return out.String()
}

func TestClaudeAlignmentNativeCLILab(t *testing.T) {
	binary := os.Getenv("CLAUDE_RECOVERY_NATIVE_CLI")
	if binary == "" {
		t.Skip("requires unmodified CLI in network-none Docker")
	}
	models := []string{"claude-sonnet-4-6", "claude-sonnet-5-5", "claude-opus-5-5"}
	if selected := os.Getenv("CLAUDE_RECOVERY_LAB_MODELS"); selected != "" {
		models = strings.Split(selected, ",")
	}
	for _, model := range models {
		require.True(t, claude2292VerifiedModel(model), "unsupported recovery lab model: %s", model)
		for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			t.Run(model+"/"+kind, func(t *testing.T) { runClaudeRecoveryNativeCLILab(t, binary, kind, model) })
		}
	}
}
