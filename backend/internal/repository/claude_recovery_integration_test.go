//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func recoveryTestScope(t *testing.T) service.RecoveryScope {
	t.Helper()
	scope := service.RecoveryScope{UserID: 987654321, GroupID: 987654322, ClientSession: uuid.NewString()}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM claude_session_ownership WHERE session_id IN(SELECT s.session_id FROM claude_recovery_segments s JOIN claude_recovery_conversations c ON s.conversation_id=c.id WHERE c.client_session_id=$1)`, scope.ClientSession)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM claude_recovery_operations WHERE conversation_id IN(SELECT id FROM claude_recovery_conversations WHERE client_session_id=$1)`, scope.ClientSession)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM claude_recovery_segments WHERE conversation_id IN(SELECT id FROM claude_recovery_conversations WHERE client_session_id=$1)`, scope.ClientSession)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM claude_recovery_conversations WHERE client_session_id=$1`, scope.ClientSession)
	})
	return scope
}
func recoveryAcquire(scope service.RecoveryScope) service.RecoveryAcquire {
	return service.RecoveryAcquire{Scope: scope, Route: "messages", Model: "claude-sonnet-4-6", Digest: "digest", Lease: time.Minute, Retention: time.Hour}
}

func TestClaudeRecoveryStoreConcurrentScopeAndFence(t *testing.T) {
	ctx := context.Background()
	repo := newAccountRepositoryWithSQL(nil, integrationDB, nil)
	scope := recoveryTestScope(t)
	const n = 24
	rows := make([]*service.RecoveryRow, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			rows[i], errs[i] = repo.AcquireRecovery(ctx, recoveryAcquire(scope))
		}(i)
	}
	close(start)
	wg.Wait()
	var winner *service.RecoveryRow
	for i := range n {
		if errs[i] == nil {
			require.Nil(t, winner)
			winner = rows[i]
		} else {
			require.ErrorIs(t, errs[i], service.ErrRecoveryBusy)
		}
	}
	require.NotNil(t, winner)
	for _, other := range []service.RecoveryScope{{UserID: scope.UserID + 1, GroupID: scope.GroupID, ClientSession: scope.ClientSession}, {UserID: scope.UserID, GroupID: scope.GroupID + 1, ClientSession: scope.ClientSession}, recoveryTestScope(t)} {
		row, e := repo.AcquireRecovery(ctx, recoveryAcquire(other))
		require.NoError(t, e)
		require.NotEqual(t, winner.ID, row.ID)
		require.NotEqual(t, winner.Session, row.Session)
		forged := *winner
		forged.Scope = other
		require.ErrorIs(t, repo.MarkRecoverySent(ctx, forged), service.ErrRecoveryConflict)
		require.ErrorIs(t, repo.BindRecoveryAccount(ctx, forged, 1, "principal", false), service.ErrRecoveryConflict)
		require.ErrorIs(t, repo.FinishRecovery(ctx, service.RecoveryFinish{Row: forged, State: "completed", Retention: time.Hour}), service.ErrRecoveryConflict)
	}
	require.NoError(t, repo.BindRecoveryAccount(ctx, *winner, 1, "principal", false))
	require.NoError(t, repo.MarkRecoverySent(ctx, *winner))
	require.ErrorIs(t, repo.MarkRecoverySent(ctx, *winner), service.ErrRecoveryConflict)
	_, e := integrationDB.ExecContext(ctx, `UPDATE claude_recovery_conversations SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, winner.ID)
	require.NoError(t, e)
	_, e = repo.AcquireRecovery(ctx, recoveryAcquire(scope))
	require.ErrorIs(t, e, service.ErrRecoveryUncertain)
	require.Error(t, repo.FinishRecovery(ctx, service.RecoveryFinish{Row: *winner, State: "completed", Retention: time.Hour}), "expired worker must not publish")
}

func TestClaudeRecoveryStoreMigrationAndTombstones(t *testing.T) {
	ctx := context.Background()
	repo := newAccountRepositoryWithSQL(nil, integrationDB, nil)
	scope := recoveryTestScope(t)
	p, e := repo.AcquireRecovery(ctx, recoveryAcquire(scope))
	require.NoError(t, e)
	require.NoError(t, repo.BindRecoveryAccount(ctx, *p, 1, "A", false))
	_, e = repo.ClaimClaudeSessionAccountID(ctx, p.Session, 1)
	require.NoError(t, e)
	p.AccountID = 1
	next := uuid.NewString()
	moved, e := repo.MigrateRecovery(ctx, *p, next, 2, "B", []byte("encrypted-checkpoint"), "disabled")
	require.NoError(t, e)
	require.Equal(t, int64(2), moved.Generation)
	require.Error(t, repo.MarkRecoverySent(ctx, *p))
	_, e = repo.MigrateRecovery(ctx, *p, uuid.NewString(), 3, "C", nil, "race")
	require.Error(t, e)
	owner, e := repo.GetClaudeSessionAccountID(ctx, p.Session)
	require.NoError(t, e)
	require.Equal(t, int64(1), owner)
	owner, e = repo.GetClaudeSessionAccountID(ctx, next)
	require.NoError(t, e)
	require.Equal(t, int64(2), owner)
	require.NoError(t, repo.MarkRecoverySent(ctx, *moved))
	_, e = repo.MigrateRecovery(ctx, *moved, uuid.NewString(), 3, "C", nil, "unsafe replay")
	require.ErrorIs(t, e, service.ErrRecoveryUncertain)
	require.NoError(t, repo.FinishRecovery(ctx, service.RecoveryFinish{Row: *moved, State: "completed", Material: []byte("encrypted-history"), Source: []byte("encrypted-source"), Retention: time.Hour}))
	_, e = integrationDB.ExecContext(ctx, `UPDATE claude_recovery_conversations SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, p.ID)
	require.NoError(t, e)
	require.NoError(t, repo.PurgeRecoveryMaterial(ctx))
	var material, restore []byte
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT c.material,s.restore FROM claude_recovery_conversations c JOIN claude_recovery_segments s ON c.active_session_id=s.session_id WHERE c.id=$1`, p.ID).Scan(&material, &restore))
	require.Nil(t, material)
	require.Nil(t, restore)
	owner, e = repo.GetClaudeSessionAccountID(ctx, next)
	require.NoError(t, e)
	require.Equal(t, int64(2), owner)
	_, e = repo.AcquireRecovery(ctx, recoveryAcquire(scope))
	require.ErrorIs(t, e, service.ErrRecoveryExpired)
}

func TestClaudeRecoveryStoreReplayAndSummaryIsolation(t *testing.T) {
	ctx := context.Background()
	repo := newAccountRepositoryWithSQL(nil, integrationDB, nil)
	scope := recoveryTestScope(t)
	a := recoveryAcquire(scope)
	a.Idempotency = "logical-operation"
	p, e := repo.AcquireRecovery(ctx, a)
	require.NoError(t, e)
	require.NoError(t, repo.MarkRecoverySent(ctx, *p))
	require.NoError(t, repo.FinishRecovery(ctx, service.RecoveryFinish{Row: *p, State: "completed", Result: []byte("encrypted-result"), Source: []byte("encrypted-source"), Retention: time.Hour}))
	replay, e := repo.AcquireRecovery(ctx, a)
	require.NoError(t, e)
	require.Equal(t, []byte("encrypted-result"), replay.Replay)
	a.Digest = "different"
	_, e = repo.AcquireRecovery(ctx, a)
	require.ErrorIs(t, e, service.ErrRecoveryConflict)
	job, e := repo.ClaimRecoverySummary(ctx, time.Minute, []int64{scope.GroupID}, 3)
	require.NoError(t, e)
	require.NotNil(t, job)
	require.Equal(t, scope, job.Row.Scope)
	forged := *job
	forged.Row.Scope.UserID++
	require.Error(t, repo.SaveRecoverySummary(ctx, forged, []byte("wrong"), true))
	require.NoError(t, repo.SaveRecoverySummary(ctx, *job, []byte("encrypted-summary"), true))
	require.Error(t, repo.SaveRecoverySummary(ctx, *job, []byte("late stale write"), true))
}

type recoveryAccounts struct {
	service.AccountRepository
	service.ClaudeRecoveryStore
	service.ClaudeSessionStore
	accounts map[int64]*service.Account
}

func (r *recoveryAccounts) GetByID(_ context.Context, id int64) (*service.Account, error) {
	a := r.accounts[id]
	if a == nil {
		return nil, service.ErrAccountNotFound
	}
	copy := *a
	return &copy, nil
}
func (r *recoveryAccounts) ListSchedulableByGroupIDAndPlatforms(_ context.Context, g int64, _ []string) ([]service.Account, error) {
	var out []service.Account
	for _, a := range r.accounts {
		if a.Status == service.StatusActive && a.Schedulable {
			for _, group := range a.GroupIDs {
				if group == g {
					out = append(out, *a)
				}
			}
		}
	}
	return out, nil
}

type recoveryGroups struct {
	service.GroupRepository
	group *service.Group
}

func (g *recoveryGroups) GetByIDLite(context.Context, int64) (*service.Group, error) {
	return g.group, nil
}

type recoverySummary struct{ seen []service.RecoveryHistory }

func (s *recoverySummary) Summarize(_ context.Context, h service.RecoveryHistory) (service.RecoveryCheckpoint, service.RecoverySummaryUsage, error) {
	s.seen = append(s.seen, h)
	cp := service.RecoveryCheckpoint{ConfigHash: h.ConfigHash, Summary: gjson.GetBytes(h.Messages[0], "content").String()}
	for _, msg := range h.Messages[:len(h.Messages)-1] {
		var obj map[string]any
		_ = json.Unmarshal(msg, &obj)
		if text, ok := obj["content"].(string); ok {
			obj["content"] = []any{map[string]any{"type": "text", "text": text}}
		}
		canonical, _ := json.Marshal(obj)
		hash := sha256.Sum256(canonical)
		cp.PrefixHashes = append(cp.PrefixHashes, hex.EncodeToString(hash[:]))
	}
	return cp, service.RecoverySummaryUsage{InputTokens: 10, OutputTokens: 5, Confirmed: true}, nil
}

type recoveryUpstream struct {
	accounts []int64
	sessions []string
	bodies   [][]byte
}

func (u *recoveryUpstream) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	b, e := io.ReadAll(req.Body)
	if e != nil {
		return nil, e
	}
	if strings.Contains(req.URL.Path, "count_tokens") {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"input_tokens":128}`))}, nil
	}
	u.accounts = append(u.accounts, id)
	u.sessions = append(u.sessions, req.Header.Get("X-Claude-Code-Session-Id"))
	u.bodies = append(u.bodies, b)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"msg_fake","type":"message","role":"assistant","content":[{"type":"text","text":"OK"}],"model":"claude-sonnet-4-6","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))}, nil
}
func (u *recoveryUpstream) DoWithTLS(r *http.Request, p string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(r, p, id, n)
}

func TestClaudeManagedRecoveryEndToEndNeverMixesSessions(t *testing.T) {
	ctx := context.Background()
	scopeA, scopeB := recoveryTestScope(t), recoveryTestScope(t)
	repo := newAccountRepositoryWithSQL(nil, integrationDB, nil)
	accounts := &recoveryAccounts{ClaudeRecoveryStore: repo, ClaudeSessionStore: repo, accounts: map[int64]*service.Account{}}
	for _, id := range []int64{101, 102} {
		accounts.accounts[id] = &service.Account{ID: id, Type: service.AccountTypeAPIKey, Platform: service.PlatformAnthropic, Status: service.StatusActive, Schedulable: true, Priority: int(id), Concurrency: 1, GroupIDs: []int64{scopeA.GroupID}, Credentials: map[string]any{"api_key": "synthetic-only"}, Extra: map[string]any{"anthropic_passthrough": true}}
	}
	cfg := &config.Config{}
	cfg.RunMode = config.RunModeSimple
	rc := config.ClaudeRecoveryConfig{Enabled: true, GroupIDs: []int64{scopeA.GroupID}, EncryptionKey: base64.StdEncoding.EncodeToString(make([]byte, 32)), SummaryAPIKey: "synthetic", SummaryModel: "fake-summary", MaxSummaryCallsPerUserDay: 1000}.WithDefaults()
	cfg.Gateway.ClaudeRecovery = rc
	groups := &recoveryGroups{group: &service.Group{ID: scopeA.GroupID, Platform: service.PlatformAnthropic, Status: service.StatusActive, Hydrated: true}}
	up := &recoveryUpstream{}
	gateway := service.NewGatewayService(accounts, groups, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, up, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	t.Cleanup(gateway.ClaudeRecovery().Close)
	summary := &recoverySummary{}
	manager := service.NewClaudeRecoveryService(rc, repo, summary)
	manager.Close() // deterministic foreground fallback; no network worker
	histories := map[string][]map[string]any{}
	send := func(scope service.RecoveryScope, marker string) *service.ClaudeRecoveryExchange {
		history := histories[scope.ClientSession]
		history = append(history, map[string]any{"role": "user", "content": marker})
		input, e := json.Marshal(map[string]any{"model": "claude-sonnet-4-6", "max_tokens": 8, "messages": history})
		require.NoError(t, e)
		exchange, e := manager.Begin(ctx, gateway, scope, input, "messages", "", false)
		require.NoError(t, e)
		accountID := exchange.Row.AccountID
		if accountID == 0 {
			accountID = 101
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/messages", nil).WithContext(exchange.Context(ctx))
		c.Request.Header.Set("X-Claude-Code-Session-Id", exchange.Row.Session)
		parsed, e := service.ParseGatewayRequest(service.NewRequestBodyRef(exchange.Body), service.PlatformAnthropic)
		require.NoError(t, e)
		_, e = gateway.Forward(c.Request.Context(), c, accounts.accounts[accountID], parsed)
		require.NoError(t, e)
		require.NoError(t, exchange.Finish(ctx, 200, http.Header{"Content-Type": {"application/json"}}, []byte(`{"role":"assistant","content":[{"type":"text","text":"OK"}]}`), true, ""))
		histories[scope.ClientSession] = append(history, map[string]any{"role": "assistant", "content": "OK"})
		return exchange
	}
	firstA := send(scopeA, "PRIVATE_SESSION_A")
	firstB := send(scopeB, "PRIVATE_SESSION_B")
	require.NotEqual(t, firstA.Row.Session, firstB.Row.Session)
	accounts.accounts[101].Schedulable = false
	movedA := send(scopeA, "continue A")
	movedB := send(scopeB, "continue B")
	require.Equal(t, []int64{101, 101, 102, 102}, up.accounts)
	require.NotEqual(t, firstA.Row.Session, movedA.Row.Session)
	require.NotEqual(t, movedA.Row.Session, movedB.Row.Session)
	require.Contains(t, string(up.bodies[2]), "PRIVATE_SESSION_A")
	require.NotContains(t, string(up.bodies[2]), "PRIVATE_SESSION_B")
	require.Contains(t, string(up.bodies[3]), "PRIVATE_SESSION_B")
	require.NotContains(t, string(up.bodies[3]), "PRIVATE_SESSION_A")
	require.NotContains(t, string(up.bodies[2]), firstA.Row.Session)
	send(scopeA, "another A turn")
	require.Equal(t, movedA.Row.Session, up.sessions[4])
	require.NotContains(t, string(up.bodies[4]), "PRIVATE_SESSION_B")
	for _, session := range []string{firstA.Row.Session, firstB.Row.Session} {
		owner, e := repo.GetClaudeSessionAccountID(ctx, session)
		require.NoError(t, e)
		require.Equal(t, int64(101), owner)
	}
	// Tampering a stored ciphertext from B into A must fail before any upstream send.
	_, e := integrationDB.ExecContext(ctx, `UPDATE claude_recovery_conversations SET material=(SELECT material FROM claude_recovery_conversations WHERE id=$2) WHERE id=$1`, movedA.Row.ID, movedB.Row.ID)
	require.NoError(t, e)
	input, _ := json.Marshal(map[string]any{"model": "claude-sonnet-4-6", "messages": append(histories[scopeA.ClientSession], map[string]any{"role": "user", "content": "after tampering"})})
	_, e = manager.Begin(ctx, gateway, scopeA, input, "messages", "", false)
	require.ErrorIs(t, e, service.ErrRecoveryConflict)
	require.Len(t, up.accounts, 5)
	require.Len(t, summary.seen, 2)
}

func TestClaudeRecoveryStorePreparedLeaseCanRecoverWithoutReplayingSentWork(t *testing.T) {
	ctx := context.Background()
	repo := newAccountRepositoryWithSQL(nil, integrationDB, nil)
	scope := recoveryTestScope(t)
	a := recoveryAcquire(scope)
	old, e := repo.AcquireRecovery(ctx, a)
	require.NoError(t, e)
	_, e = integrationDB.ExecContext(ctx, `UPDATE claude_recovery_conversations SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, old.ID)
	require.NoError(t, e)
	fresh, e := repo.AcquireRecovery(ctx, a)
	require.NoError(t, e)
	require.NotEqual(t, old.Lease, fresh.Lease)
	require.Error(t, repo.MarkRecoverySent(ctx, *old))
	require.NoError(t, repo.MarkRecoverySent(ctx, *fresh))
	require.NoError(t, repo.FinishRecovery(ctx, service.RecoveryFinish{Row: *fresh, State: "uncertain", Retention: time.Hour}))
	_, e = repo.AcquireRecovery(ctx, a)
	require.True(t, errors.Is(e, service.ErrRecoveryUncertain), fmt.Sprint(e))
}
