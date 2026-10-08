//go:build unit

package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type disabledRecoveryWorkerStore struct {
	ClaudeRecoveryStore
	purged       chan struct{}
	summaryCalls atomic.Int64
}

func (s *disabledRecoveryWorkerStore) PurgeRecoveryMaterial(context.Context) error {
	select {
	case s.purged <- struct{}{}:
	default:
	}
	return nil
}
func (s *disabledRecoveryWorkerStore) ClaimRecoverySummary(context.Context, time.Duration, []int64, int) (*RecoverySummaryJob, error) {
	s.summaryCalls.Add(1)
	return nil, nil
}
func TestRecoveryDisabledServiceStillPurgesWithoutCallingSummaries(t *testing.T) {
	store := &disabledRecoveryWorkerStore{purged: make(chan struct{}, 1)}
	manager := NewClaudeRecoveryService(config.ClaudeRecoveryConfig{}, store)
	defer manager.Close()
	manager.Start()
	select {
	case <-store.purged:
	case <-time.After(5 * time.Second):
		t.Fatal("retention cleanup did not run")
	}
	require.Zero(t, store.summaryCalls.Load())
}

func TestRecoveryCipherAuthenticatesTenantConversationAndPurpose(t *testing.T) {
	box, e := newRecoveryCipher(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	require.NoError(t, e)
	row := RecoveryRow{ID: uuid.NewString(), Scope: RecoveryScope{UserID: 1, GroupID: 2, ClientSession: uuid.NewString()}}
	encrypted, e := box.seal(row, "history", map[string]string{"secret": "SESSION_A_ONLY"})
	require.NoError(t, e)
	require.NotContains(t, string(encrypted), "SESSION_A_ONLY")
	var decoded map[string]string
	require.NoError(t, box.open(row, "history", encrypted, &decoded))
	require.Equal(t, "SESSION_A_ONLY", decoded["secret"])
	for _, change := range []func(*RecoveryRow){func(p *RecoveryRow) { p.Scope.UserID++ }, func(p *RecoveryRow) { p.Scope.GroupID++ }, func(p *RecoveryRow) { p.Scope.ClientSession = uuid.NewString() }, func(p *RecoveryRow) { p.ID = uuid.NewString() }} {
		other := row
		change(&other)
		require.ErrorIs(t, box.open(other, "history", encrypted, &decoded), ErrRecoveryConflict)
	}
	require.ErrorIs(t, box.open(row, "checkpoint", encrypted, &decoded), ErrRecoveryConflict)
	encrypted[len(encrypted)-1] ^= 1
	require.ErrorIs(t, box.open(row, "history", encrypted, &decoded), ErrRecoveryConflict)
}

func TestRecoveryHistoryAnchorsRejectForksAndKeepFixedPrefix(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"original task"},{"role":"assistant","content":"observed result"},{"role":"user","content":"continue"}]}`)
	h, e := parseRecoveryHistory(body, "messages", 65536)
	require.NoError(t, e)
	require.True(t, recoverySafeBoundary(h))
	cp := &RecoveryCheckpoint{ConfigHash: h.ConfigHash, Summary: "original task", PrefixHashes: []string{recoveryMessageHash(h.Messages[0], h.Route), recoveryMessageHash(h.Messages[1], h.Route)}}
	output, e := recoveryApplyCheckpoint(body, h, cp)
	require.NoError(t, e)
	require.Len(t, gjson.GetBytes(output, "messages").Array(), 2)
	require.Equal(t, "continue", gjson.GetBytes(output, "messages.1.content").String())
	fork := h
	fork.Messages = append([]json.RawMessage(nil), h.Messages...)
	fork.Messages[0] = json.RawMessage(`{"role":"user","content":"OTHER_SESSION_DATA"}`)
	_, e = recoveryApplyCheckpoint(body, fork, cp)
	require.ErrorIs(t, e, ErrRecoveryConflict)
	require.False(t, recoveryHistoryExtends(h, fork))
	shortened := h
	shortened.Messages = h.Messages[:1]
	require.False(t, recoveryHistoryExtends(h, shortened))
	changed := h
	changed.ConfigHash = "new-tools"
	_, e = recoveryApplyCheckpoint(body, changed, cp)
	require.ErrorIs(t, e, ErrRecoveryConflict)
}

func TestRecoveryToolAndProtocolBoundaries(t *testing.T) {
	cases := []struct {
		name, body, route string
		safe              bool
	}{
		{"messages closed", `{"model":"claude-sonnet-4-6","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"read","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"done"}]},{"role":"assistant","content":"finished"},{"role":"user","content":"next"}]}`, "messages", true},
		{"messages in tool turn", `{"model":"claude-sonnet-4-6","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t1"}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"done"}]}]}`, "messages", false},
		{"unknown tool result", `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"foreign","content":"done"}]},{"role":"user","content":"next"}]}`, "messages", false},
		{"chat pending", `{"model":"claude-sonnet-4-6","messages":[{"role":"assistant","tool_calls":[{"id":"t1","type":"function","function":{"name":"write","arguments":"{}"}}]},{"role":"user","content":"next"}]}`, "chat", false},
		{"responses closed", `{"model":"claude-sonnet-4-6","input":[{"type":"function_call","call_id":"t1"},{"type":"function_call_output","call_id":"t1","output":"done"},{"role":"user","content":"next"}]}`, "responses", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, e := parseRecoveryHistory([]byte(tc.body), tc.route, 65536)
			require.NoError(t, e)
			require.Equal(t, tc.safe, recoverySafeBoundary(h))
		})
	}
	_, e := parseRecoveryHistory([]byte(`{"model":"claude-sonnet-4-6","previous_response_id":"foreign","input":"next"}`), "responses", 65536)
	require.Error(t, e)
	_, e = recoveryAttachments([]json.RawMessage{json.RawMessage(`{"role":"user","content":[{"type":"image","source":{"type":"url","url":"https://untrusted.invalid"}}]}`)})
	require.Error(t, e)
}

func TestRecoverySummaryValidatesEvidenceAndUsesIndependentCredentials(t *testing.T) {
	h, e := parseRecoveryHistory([]byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"Keep the file unchanged"},{"role":"assistant","content":[{"type":"thinking","thinking":"opaque content","signature":"PRIVATE_SIGNATURE"},{"type":"text","text":"Read-only inspection completed"}]},{"role":"user","content":"next"}]}`), "messages", 65536)
	require.NoError(t, e)
	for _, bad := range []bool{false, true} {
		t.Run(map[bool]string{false: "grounded", true: "invented quote"}[bad], func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "summary-key", r.Header.Get("x-api-key"))
				require.Empty(t, r.Header.Get("Authorization"))
				require.Empty(t, r.Header.Get("X-Claude-Code-Session-Id"))
				var req map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
				data, _ := json.Marshal(req)
				require.NotContains(t, string(data), "PRIVATE_SIGNATURE")
				require.NotContains(t, string(data), "opaque content")
				quote := "Keep the file unchanged"
				if bad {
					quote = "The file was deleted"
				}
				text, _ := json.Marshal(map[string]any{"summary": "Preserve the file; inspection finished.", "facts": []any{map[string]any{"index": 0, "quote": quote}}})
				require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": string(text)}}, "stop_reason": "end_turn", "usage": map[string]int{"input_tokens": 20, "output_tokens": 10}}))
			}))
			defer server.Close()
			cfg := config.ClaudeRecoveryConfig{SummaryURL: server.URL, SummaryAPIKey: "summary-key", SummaryModel: "test"}.WithDefaults()
			s := newRecoveryHTTPSummarizer(cfg)
			s.client = server.Client()
			cp, usage, e := s.Summarize(context.Background(), h)
			if bad {
				require.Error(t, e)
			} else {
				require.NoError(t, e)
				require.Len(t, cp.PrefixHashes, 2)
				require.Equal(t, "Keep the file unchanged", cp.Facts[0].Quote)
			}
			require.True(t, usage.Confirmed)
		})
	}
}

func TestRecoveryConfigRequiresExplicitOptInAndSecrets(t *testing.T) {
	require.NoError(t, (config.ClaudeRecoveryConfig{}).Validate())
	c := config.ClaudeRecoveryConfig{Enabled: true}
	require.Error(t, c.Validate())
	c.EncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	c.GroupIDs = []int64{1}
	c.SummaryAPIKey = "private-key"
	c.SummaryModel = "test"
	require.NoError(t, c.Validate())
	encoded, e := json.Marshal(c)
	require.NoError(t, e)
	require.NotContains(t, string(encoded), "private-key")
	require.NotContains(t, string(encoded), c.EncryptionKey)
	c.SummaryURL = "http://example.invalid"
	require.Error(t, c.Validate())
	c.SummaryURL = "https://user:password@example.invalid"
	require.Error(t, c.Validate())
}

func TestRecoveryModelAndBudgetLimits(t *testing.T) {
	_, e := parseRecoveryHistory([]byte(`{"model":"unknown-model","messages":[{"role":"user","content":"hi"}]}`), "messages", 4096)
	require.Error(t, e)
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"` + strings.Repeat("x", 5000) + `"}]}`)
	_, e = parseRecoveryHistory(body, "messages", 4096)
	require.Error(t, e)
}

func TestRecoveryRecordsActualReplyAndRejectsForeignAssistantHistory(t *testing.T) {
	h, e := parseRecoveryHistory([]byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"task A"}]}`), "messages", 65536)
	require.NoError(t, e)
	committed, e := recoveryCompletedHistory(h, []byte(`{"role":"assistant","content":[{"type":"text","text":"reply A"}]}`), false)
	require.NoError(t, e)
	next, e := parseRecoveryHistory([]byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":[{"type":"text","text":"task A","cache_control":{"type":"ephemeral"}}]},{"role":"assistant","content":"reply A"},{"role":"user","content":"next"}]}`), "messages", 65536)
	require.NoError(t, e)
	require.True(t, recoveryHistoryExtends(committed, next))
	next.Messages[1] = json.RawMessage(`{"role":"assistant","content":"reply B from another session"}`)
	require.False(t, recoveryHistoryExtends(committed, next))
	_, e = recoveryCompletedHistory(h, []byte(`{"error":"not a completed reply"}`), false)
	require.Error(t, e)
}

func TestRecoveryStreamReconstructionPreservesSignedToolRoundTrip(t *testing.T) {
	h, e := parseRecoveryHistory([]byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"inspect"}]}`), "messages", 65536)
	require.NoError(t, e)
	stream := `data: {"type":"message_start","message":{"role":"assistant","content":[]}}

data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"signed-A"}}

data: {"type":"content_block_stop","index":0}

data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tool-A","name":"Read","input":{}}}

data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.txt\"}"}}

data: {"type":"content_block_stop","index":1}

data: {"type":"message_stop"}

`
	committed, e := recoveryCompletedHistory(h, []byte(stream), true)
	require.NoError(t, e)
	next := committed
	next.Messages = append(append([]json.RawMessage(nil), committed.Messages...), json.RawMessage(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-A","content":"data"}]}`))
	require.True(t, recoveryHistoryExtends(committed, next))
	require.False(t, recoverySafeBoundary(next))
	next.Messages[1] = json.RawMessage(strings.ReplaceAll(string(next.Messages[1]), "signed-A", "signed-B"))
	require.False(t, recoveryHistoryExtends(committed, next))
	_, e = recoveryCompletedHistory(h, []byte(strings.ReplaceAll(stream, `data: {"type":"message_stop"}`, "")), true)
	require.Error(t, e)
}

func TestRecoveryMessageHashDoesNotStripToolInputFields(t *testing.T) {
	a := json.RawMessage(`{"role":"assistant","content":[{"type":"tool_use","id":"t","name":"configure","input":{"cache_control":"private"}}]}`)
	b := json.RawMessage(`{"role":"assistant","content":[{"type":"tool_use","id":"t","name":"configure","input":{"cache_control":"public"}}]}`)
	require.NotEqual(t, recoveryMessageHash(a, "messages"), recoveryMessageHash(b, "messages"))
}

func TestRecoveryRejectsAmbiguousIdentityJSON(t *testing.T) {
	for _, raw := range []string{
		`{"model":"claude-sonnet-4-6","model":"other","messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi","content":"other"}]}`,
		`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"metadata":{"user_id":"{\"device_id\":\"d\",\"session_id\":\"11111111-1111-4111-8111-111111111111\",\"session_id\":\"22222222-2222-4222-8222-222222222222\"}"}}`,
	} {
		_, e := parseRecoveryHistory([]byte(raw), "messages", 65536)
		require.ErrorIs(t, e, ErrRecoveryConflict)
	}
	require.NoError(t, recoveryUniqueJSON([]byte(`{"text":"{\"k\":1,\"k\":2}"}`)), "quoted user text is not parsed as control JSON")
}

func TestRecoveryPreservesInlineAttachmentsAndPrivilegedMessages(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"system","content":"Never write files"},{"role":"user","content":[{"type":"text","text":"Keep these bytes"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAEC"}}]},{"role":"assistant","content":"observed"},{"role":"user","content":"continue"}]}`)
	h, e := parseRecoveryHistory(body, "chat", 65536)
	require.NoError(t, e)
	attachments, e := recoveryAttachments(h.Messages[:3])
	require.NoError(t, e)
	require.Len(t, attachments, 1)
	cp := RecoveryCheckpoint{ConfigHash: h.ConfigHash, Summary: "A prior observation", Attachments: attachments, Critical: recoveryCriticalInstructions(h.Messages[:3])}
	for _, message := range h.Messages[:3] {
		cp.PrefixHashes = append(cp.PrefixHashes, recoveryMessageHash(message, h.Route))
	}
	output, e := recoveryApplyCheckpoint(body, h, &cp)
	require.NoError(t, e)
	require.Equal(t, "system", gjson.GetBytes(output, "messages.0.role").String())
	require.Equal(t, "Never write files", gjson.GetBytes(output, "messages.0.content").String())
	require.Contains(t, string(output), "data:image/png;base64,AAEC")
	require.Contains(t, string(output), "Keep these bytes")
	require.Equal(t, "continue", gjson.GetBytes(output, "messages.2.content").String())
}

func TestRecoveryIncrementalSummaryCarriesEvidenceWithoutResendingOldOutput(t *testing.T) {
	h, e := parseRecoveryHistory([]byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"Keep files read-only"},{"role":"assistant","content":"OLD_OUTPUT_NOT_RESENT"},{"role":"user","content":"new step"},{"role":"assistant","content":"new result"},{"role":"user","content":""}]}`), "messages", 65536)
	require.NoError(t, e)
	base := &RecoveryCheckpoint{ConfigHash: h.ConfigHash, Summary: "Earlier inspection", Facts: []RecoveryFact{{Index: 0, Quote: "Keep files read-only"}}}
	for _, message := range h.Messages[:2] {
		base.PrefixHashes = append(base.PrefixHashes, recoveryMessageHash(message, h.Route))
	}
	h.SummaryBase = base
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		encoded, _ := json.Marshal(request)
		require.NotContains(t, string(encoded), "OLD_OUTPUT_NOT_RESENT")
		require.Contains(t, string(encoded), "Earlier inspection")
		require.Contains(t, string(encoded), "new result")
		text := `{"summary":"Earlier inspection and new result","facts":[{"index":3,"quote":"new result"}]}`
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"stop_reason": "end_turn", "content": []any{map[string]any{"type": "text", "text": text}}, "usage": map[string]int{"input_tokens": 10, "output_tokens": 5}}))
	}))
	defer server.Close()
	summarizer := newRecoveryHTTPSummarizer(config.ClaudeRecoveryConfig{SummaryURL: server.URL, SummaryAPIKey: "fake", SummaryModel: "fake"}.WithDefaults())
	summarizer.client = server.Client()
	cp, _, e := summarizer.Summarize(context.Background(), h)
	require.NoError(t, e)
	require.Len(t, cp.PrefixHashes, 4)
	require.Contains(t, cp.Facts, RecoveryFact{Index: 0, Quote: "Keep files read-only"})
	require.Len(t, cp.Critical, 2)
}

func TestRecoveryOutcomeClassificationNeverReplaysUncertainWork(t *testing.T) {
	for _, tc := range []struct {
		name             string
		upstream, status int
		complete         bool
		state            string
	}{
		{"transport", 0, 502, false, "uncertain"}, {"server error", 500, 500, false, "uncertain"}, {"unavailable", 503, 503, false, "uncertain"}, {"partial", 200, 200, false, "uncertain"}, {"permission", 403, 403, false, "ready"}, {"rate limited", 429, 429, false, "ready"}, {"overloaded", 529, 529, false, "ready"}, {"completed", 200, 200, true, "ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &recoveryLabStore{rows: map[RecoveryScope]RecoveryRow{}, sent: map[string]bool{}}
			manager := NewClaudeRecoveryService(config.ClaudeRecoveryConfig{Enabled: true, GroupIDs: []int64{7}, EncryptionKey: base64.StdEncoding.EncodeToString(make([]byte, 32)), SummaryAPIKey: "fake", SummaryModel: "fake"}, store, recoveryLabSummary{})
			manager.Close()
			scope := RecoveryScope{UserID: 1, GroupID: 7, ClientSession: uuid.NewString()}
			p, e := manager.Begin(context.Background(), nil, scope, []byte(`{"model":"claude-sonnet-4-6","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`), "messages", "", false)
			require.NoError(t, e)
			a := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, GroupIDs: []int64{7}, Credentials: map[string]any{"api_key": "fake"}}
			require.NoError(t, p.BeforeSend(context.Background(), a))
			require.Error(t, p.BeforeSend(context.Background(), a), "second send is forbidden")
			p.ResponseStatus.Store(int64(tc.upstream))
			require.NoError(t, p.Finish(context.Background(), tc.status, nil, []byte(`{"role":"assistant","content":[{"type":"text","text":"OK"}]}`), tc.complete, ""))
			require.Equal(t, tc.state, store.rows[scope].State)
		})
	}
}
