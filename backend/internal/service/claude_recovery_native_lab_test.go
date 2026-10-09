//go:build unit

package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Test-only port for the isolated native executable experiment. Transaction,
// lease, replay and durability properties are tested against PostgreSQL separately.
type recoveryLabStore struct {
	ClaudeRecoveryStore
	memoryClaudeSessionStore
	mu   sync.Mutex
	rows map[RecoveryScope]RecoveryRow
	sent map[string]bool
}

func (s *recoveryLabStore) AcquireRecovery(_ context.Context, a RecoveryAcquire) (*RecoveryRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[a.Scope]
	if !ok {
		r = RecoveryRow{ID: uuid.NewString(), Scope: a.Scope, Route: a.Route, Model: a.Model, Session: uuid.NewString(), Generation: 1, State: "ready"}
	}
	if r.State != "ready" {
		return nil, ErrRecoveryBusy
	}
	if !a.ReadOnly {
		r.Lease, r.Operation, r.State = uuid.NewString(), uuid.NewString(), "busy"
	}
	s.rows[a.Scope] = r
	return &r, nil
}
func (s *recoveryLabStore) BindRecoveryAccount(_ context.Context, p RecoveryRow, id int64, principal string, readOnly bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rows[p.Scope]
	if r.ID != p.ID || r.Session != p.Session || r.Generation != p.Generation || (!readOnly && r.Lease != p.Lease) {
		return ErrRecoveryConflict
	}
	if r.AccountID != 0 && (r.AccountID != id || r.Principal != principal) {
		return ErrRecoveryConflict
	}
	r.AccountID, r.Principal = id, principal
	s.rows[p.Scope] = r
	return nil
}
func (s *recoveryLabStore) MarkRecoverySent(_ context.Context, p RecoveryRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rows[p.Scope]
	if r.Lease != p.Lease || r.Session != p.Session || s.sent[p.Operation] {
		return ErrRecoveryConflict
	}
	s.sent[p.Operation] = true
	return nil
}
func (*recoveryLabStore) RenewRecoveryLease(context.Context, RecoveryRow, time.Duration) error {
	return nil
}
func (s *recoveryLabStore) FinishRecovery(_ context.Context, f RecoveryFinish) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rows[f.Row.Scope]
	if r.ID != f.Row.ID || r.Lease != f.Row.Lease || r.Session != f.Row.Session {
		return ErrRecoveryConflict
	}
	r.State = "ready"
	if f.State == "uncertain" {
		r.State = "uncertain"
	}
	if f.Material != nil {
		r.Material = append([]byte(nil), f.Material...)
	}
	if f.Source != nil {
		r.Source = append([]byte(nil), f.Source...)
	}
	if f.ResetHistory {
		r.Checkpoint = nil
		r.Source = append([]byte(nil), f.Source...)
	}
	if f.ClearRestore {
		r.Restore = nil
	}
	s.rows[r.Scope] = r
	return nil
}
func (s *recoveryLabStore) MigrateRecovery(ctx context.Context, p RecoveryRow, next string, id int64, principal string, restore []byte, _ string) (*RecoveryRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rows[p.Scope]
	if r.ID != p.ID || r.Lease != p.Lease || r.Session != p.Session || s.sent[p.Operation] {
		return nil, ErrRecoveryConflict
	}
	r.Session, r.AccountID, r.Principal, r.Restore = next, id, principal, restore
	r.Generation++
	s.rows[p.Scope] = r
	_, e := s.ClaimClaudeSessionAccountID(ctx, next, id)
	return &r, e
}
func (s *recoveryLabStore) CheckRecoveryVersion(_ context.Context, p RecoveryRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rows[p.Scope]
	if r.ID != p.ID || r.Session != p.Session || r.State != "ready" {
		return ErrRecoveryConflict
	}
	return nil
}
func (*recoveryLabStore) ReserveRecoverySummaryBudget(context.Context, int64, int) error { return nil }
func (*recoveryLabStore) RecordRecoverySummaryUsage(context.Context, int64, RecoverySummaryUsage) error {
	return nil
}
func (s *recoveryLabStore) IsRecoveryUpstreamSession(_ context.Context, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rows {
		if r.Session == id {
			return true, nil
		}
	}
	return false, nil
}
func (s *recoveryLabStore) HasRecoveryConversation(_ context.Context, scope RecoveryScope) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.rows[scope]
	return ok, nil
}

type recoveryLabSummary struct{}

func (recoveryLabSummary) Summarize(_ context.Context, h RecoveryHistory) (RecoveryCheckpoint, RecoverySummaryUsage, error) {
	cp := RecoveryCheckpoint{ConfigHash: h.ConfigHash}
	for i, m := range h.Messages[:len(h.Messages)-1] {
		text, e := recoveryVisibleText(m)
		if e != nil {
			return cp, RecoverySummaryUsage{}, e
		}
		cp.PrefixHashes = append(cp.PrefixHashes, recoveryMessageHash(m, h.Route))
		cp.Summary += text
		if i == 0 {
			cp.Facts = []RecoveryFact{{Index: 0, Quote: text}}
		}
	}
	return cp, RecoverySummaryUsage{Confirmed: true}, nil
}

type recoveryLabUpstream struct {
	records []map[string]any
	reply   func(*http.Request, []byte) string
}

func (u *recoveryLabUpstream) Do(r *http.Request, _ string, account int64, _ int) (*http.Response, error) {
	body, e := io.ReadAll(r.Body)
	if e != nil {
		return nil, e
	}
	if getHeaderRaw(r.Header, "X-Api-Key") != fmt.Sprintf("fake-account-%d", account) && getHeaderRaw(r.Header, "Authorization") != fmt.Sprintf("Bearer fake-account-%d", account) {
		return nil, fmt.Errorf("incorrect upstream credential")
	}
	u.records = append(u.records, map[string]any{"account": account, "session": getHeaderRaw(r.Header, "X-Claude-Code-Session-Id"), "body": json.RawMessage(body), "count": strings.HasSuffix(r.URL.Path, "count_tokens")})
	contentType, payload := "application/json", `{"input_tokens":128}`
	if !strings.Contains(r.URL.Path, "count_tokens") {
		contentType = "text/event-stream"
		payload = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_lab\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-6\",\"content\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"OK\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	}
	if u.reply != nil && !strings.Contains(r.URL.Path, "count_tokens") {
		payload = u.reply(r, body)
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(payload))}, nil
}
func (u *recoveryLabUpstream) DoWithTLS(r *http.Request, p string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(r, p, id, n)
}

func TestClaudeRecoveryNativeCLILab(t *testing.T) {
	binary := os.Getenv("CLAUDE_RECOVERY_NATIVE_CLI")
	if binary == "" {
		t.Skip("run with the unmodified 2.1.292 executable inside network-none Docker")
	}
	for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
		t.Run(kind, func(t *testing.T) { runClaudeRecoveryNativeCLILab(t, binary, kind) })
	}
}

func runClaudeRecoveryNativeCLILab(t *testing.T, binary, kind string, alignmentModel ...string) {
	model := "claude-sonnet-4-6"
	extended := len(alignmentModel) > 0
	if extended {
		model = alignmentModel[0]
	}
	store := &recoveryLabStore{rows: map[RecoveryScope]RecoveryRow{}, sent: map[string]bool{}}
	rc := config.ClaudeRecoveryConfig{Enabled: true, GroupIDs: []int64{7}, EncryptionKey: base64.StdEncoding.EncodeToString(make([]byte, 32)), SummaryAPIKey: "fake-summary-only", SummaryModel: "fake-model"}.WithDefaults()
	manager := NewClaudeRecoveryService(rc, store, recoveryLabSummary{})
	manager.Close()
	svc, _ := newClaudeContractGateway(t)
	svc.claudeRecovery = manager
	svc.claudeSessionStore = store
	accounts := &mockAccountRepoForPlatform{accountsByID: map[int64]*Account{}}
	for _, id := range []int64{1, 2} {
		accounts.accounts = append(accounts.accounts, Account{ID: id, Type: kind, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Priority: int(id), Concurrency: 1, GroupIDs: []int64{7}, AccountGroups: []AccountGroup{{GroupID: 7}}, Credentials: map[string]any{"api_key": fmt.Sprintf("fake-account-%d", id), "access_token": fmt.Sprintf("fake-account-%d", id)}, Extra: map[string]any{"anthropic_passthrough": true, "account_uuid": fmt.Sprintf("%08d-0000-4000-8000-000000000001", id)}})
	}
	for i := range accounts.accounts {
		accounts.accountsByID[accounts.accounts[i].ID] = &accounts.accounts[i]
	}
	svc.accountRepo = accounts
	svc.groupRepo = &mockGroupRepoForGateway{groups: map[int64]*Group{7: {ID: 7, Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true}}}
	upstream := &recoveryLabUpstream{}
	if extended {
		upstream.reply = func(r *http.Request, body []byte) string {
			marker := "PRIVATE_NATIVE_A"
			if bytes.Contains(body, []byte("PRIVATE_NATIVE_B")) {
				marker = "PRIVATE_NATIVE_B"
			}
			text := "OK " + marker
			if getHeaderRaw(r.Header, "x-claude-code-request-class") == "compaction" || recoveryLooksLikeCompactionBody(body) {
				text = "<analysis>synthetic</analysis>\n<summary>Remember " + marker + " and preserve its instructions.</summary>"
			}
			return alignmentNativeStream(model, text)
		}
	}
	svc.httpUpstream = upstream
	var mu sync.Mutex
	var problems []string
	clients := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if !strings.HasPrefix(r.URL.Path, "/v1/messages") {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		body, e := io.ReadAll(io.LimitReader(r.Body, 2*1024*1024))
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = r
		sid, e := ResolveClaudeRecoveryClientID(r.Context(), c, body)
		if e != nil || sid == "" {
			http.Error(w, "missing client session", 400)
			return
		}
		for _, marker := range []string{"PRIVATE_NATIVE_A", "PRIVATE_NATIVE_B"} {
			if bytes.Contains(body, []byte(marker)) {
				clients[marker] = sid
			}
		}
		readOnly := strings.HasSuffix(r.URL.Path, "count_tokens")
		recoveryCtx, e := WithClaudeRecoveryRequest(r.Context(), r.Header)
		if e != nil {
			http.Error(w, e.Error(), 409)
			return
		}
		p, e := manager.Begin(recoveryCtx, svc, RecoveryScope{UserID: 1, GroupID: 7, ClientSession: sid}, body, "messages", "", readOnly)
		if e != nil {
			problems = append(problems, e.Error())
			if len(problems) == 1 {
				t.Logf("native managed request rejected: model=%s class=%s: %v", model, r.Header.Get("x-claude-code-request-class"), e)
				if dir := os.Getenv("CLAUDE_RECOVERY_LAB_OUTPUT"); dir != "" {
					dir = filepath.Join(dir, model, kind)
					if os.MkdirAll(dir, 0700) == nil {
						_ = os.WriteFile(filepath.Join(dir, "first-rejected-body.json"), body, 0600)
					}
				}
			}
			http.Error(w, e.Error(), 409)
			return
		}
		c.Request = r.Clone(p.Context(r.Context()))
		c.Request.Header.Del("X-Sub2API-Session-Id")
		c.Request.Header.Set("X-Claude-Code-Session-Id", p.Row.Session)
		group := int64(7)
		account, e := svc.SelectAccountForModel(c.Request.Context(), &group, p.Row.Session, p.History.Model)
		if e == nil {
			var parsed *ParsedRequest
			parsed, e = ParseGatewayRequest(NewRequestBodyRef(p.Body), PlatformAnthropic)
			if e == nil {
				if readOnly {
					e = svc.ForwardCountTokens(c.Request.Context(), c, account, parsed)
				} else {
					_, e = svc.Forward(c.Request.Context(), c, account, parsed)
				}
			}
		}
		if e != nil {
			problems = append(problems, e.Error())
		}
		finishErr := p.Finish(context.Background(), rec.Code, rec.Header(), rec.Body.Bytes(), e == nil, "")
		if finishErr != nil {
			problems = append(problems, finishErr.Error())
		}
		for key, values := range rec.Header() {
			w.Header()[key] = values
		}
		w.Header().Set("X-Sub2API-Session-Id", sid)
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	}))
	t.Cleanup(server.Close)
	type nativeProcess struct {
		cmd    *exec.Cmd
		in     io.WriteCloser
		lines  chan string
		stderr *bytes.Buffer
	}
	start := func(marker string) *nativeProcess {
		folder := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(folder, "home"), 0700))
		args := []string{"--bare", "--model", model, "--tools", "", "--permission-mode", "default", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--setting-sources", "", "--system-prompt", "Local synthetic protocol test. Reply OK.", "--no-session-persistence", "--max-turns", "4", "--output-format", "stream-json", "--input-format", "stream-json", "--verbose", "-p"}
		if !extended {
			args = append(args, "--disable-slash-commands")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
		t.Cleanup(cancel)
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = folder
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(folder, "home"), "CLAUDE_CONFIG_DIR=" + filepath.Join(folder, "config"), "ANTHROPIC_BASE_URL=" + server.URL, "ANTHROPIC_API_KEY=sk-ant-synthetic-only", "DISABLE_AUTOUPDATER=1", "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL=1", "CLAUDE_CODE_ENABLE_TELEMETRY=0", "API_TIMEOUT_MS=5000", "TERM=dumb"}
		if extended {
			cmd.Env = append(cmd.Env, `CLAUDE_CODE_EXTRA_METADATA={"audit_label":"`+marker+`"}`)
		}
		in, e := cmd.StdinPipe()
		require.NoError(t, e)
		out, e := cmd.StdoutPipe()
		require.NoError(t, e)
		stderr := &bytes.Buffer{}
		cmd.Stderr = stderr
		lines := make(chan string, 1000)
		require.NoError(t, cmd.Start())
		go func() {
			defer close(lines)
			scanner := bufio.NewScanner(out)
			scanner.Buffer(make([]byte, 4096), 4*1024*1024)
			for scanner.Scan() {
				lines <- scanner.Text()
			}
		}()
		p := &nativeProcess{cmd: cmd, in: in, lines: lines, stderr: stderr}
		send := map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": marker + " first turn. Reply OK."}}
		require.NoError(t, json.NewEncoder(in).Encode(send))
		return p
	}
	awaitResult := func(p *nativeProcess) {
		timer := time.NewTimer(60 * time.Second)
		defer timer.Stop()
		for {
			select {
			case line, ok := <-p.lines:
				if !ok {
					t.Fatalf("native CLI exited before result: %s", p.stderr.String())
				}
				if gjson.Get(line, "type").String() == "result" {
					require.False(t, gjson.Get(line, "is_error").Bool(), line)
					return
				}
			case <-timer.C:
				mu.Lock()
				detail := append([]string(nil), problems...)
				mu.Unlock()
				t.Fatalf("native CLI result timed out: %v", detail)
			}
		}
	}
	a, b := start("PRIVATE_NATIVE_A"), start("PRIVATE_NATIVE_B")
	awaitResult(a)
	awaitResult(b)
	if extended {
		for _, prompt := range []string{"/compact", "Continue after first compaction.", "/compact Keep this conversation's marker.", "Continue after second compaction."} {
			for _, p := range []*nativeProcess{a, b} {
				require.NoError(t, json.NewEncoder(p.in).Encode(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": prompt}}))
			}
			awaitResult(a)
			awaitResult(b)
		}
	}
	mu.Lock()
	accounts.accountsByID[1].Schedulable = false
	mu.Unlock()
	for i, p := range []*nativeProcess{a, b} {
		marker := []string{"PRIVATE_NATIVE_A", "PRIVATE_NATIVE_B"}[i]
		require.NoError(t, json.NewEncoder(p.in).Encode(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": marker + " second turn. Continue and reply OK."}}))
		if !extended {
			require.NoError(t, p.in.Close())
		}
	}
	awaitResult(a)
	awaitResult(b)
	if extended {
		for _, prompt := range []string{"/compact", "Continue after compaction on the migrated account."} {
			for _, p := range []*nativeProcess{a, b} {
				require.NoError(t, json.NewEncoder(p.in).Encode(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": prompt}}))
			}
			awaitResult(a)
			awaitResult(b)
		}
		require.NoError(t, a.in.Close())
		require.NoError(t, b.in.Close())
	}
	require.NoError(t, a.cmd.Wait(), a.stderr.String())
	require.NoError(t, b.cmd.Wait(), b.stderr.String())
	mu.Lock()
	defer mu.Unlock()
	require.Empty(t, problems)
	require.Len(t, clients, 2)
	require.NotEqual(t, clients["PRIVATE_NATIVE_A"], clients["PRIVATE_NATIVE_B"])
	byMarker := map[string][]map[string]any{}
	for _, record := range upstream.records {
		raw, _ := json.Marshal(record["body"])
		for _, marker := range []string{"PRIVATE_NATIVE_A", "PRIVATE_NATIVE_B"} {
			if bytes.Contains(raw, []byte(marker)) {
				other := "PRIVATE_NATIVE_A"
				if marker == other {
					other = "PRIVATE_NATIVE_B"
				}
				require.NotContains(t, string(raw), other)
				require.NotEqual(t, clients[marker], record["session"])
				if record["count"] != true {
					byMarker[marker] = append(byMarker[marker], record)
				}
			}
		}
	}
	for marker, records := range byMarker {
		want := 2
		if extended {
			want = 8
		}
		require.Len(t, records, want, marker)
		require.Equal(t, int64(1), records[0]["account"])
		require.Equal(t, int64(2), records[want-1]["account"])
		require.NotEqual(t, records[0]["session"], records[want-1]["session"])
		if extended {
			for _, record := range records[:5] {
				require.Equal(t, records[0]["session"], record["session"])
			}
			for _, record := range records[5:] {
				require.Equal(t, records[5]["session"], record["session"])
				require.Equal(t, int64(2), record["account"])
			}
			for _, record := range records {
				body, _ := json.Marshal(record["body"])
				uid := gjson.GetBytes(body, "metadata.user_id").String()
				require.Equal(t, marker, gjson.Get(uid, "audit_label").String())
			}
		}
	}
	require.Len(t, byMarker, 2)
	if dir := os.Getenv("CLAUDE_RECOVERY_LAB_OUTPUT"); dir != "" {
		if extended {
			dir = filepath.Join(dir, model)
		}
		dir = filepath.Join(dir, kind)
		require.NoError(t, os.MkdirAll(dir, 0700))
		data, e := json.MarshalIndent(map[string]any{"clients": clients, "requests": upstream.records, "cross_session_leaks": 0, "errors": problems}, "", "  ")
		require.NoError(t, e)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "native-managed-recovery.json"), data, 0600))
	}
}
