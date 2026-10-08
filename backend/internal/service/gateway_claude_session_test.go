//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type conflictingClaudeClaim struct{ memoryClaudeSessionStore }

func (*conflictingClaudeClaim) ClaimClaudeSessionAccountID(context.Context, string, int64) (int64, error) {
	return 999, nil
}

type claudeSessionSlotRecorder struct {
	SessionLimitCache
	registered, released int64
}

func (r *claudeSessionSlotRecorder) RegisterSession(_ context.Context, id int64, _ string, _ int, _ time.Duration) (bool, error) {
	r.registered = id
	return true, nil
}
func (r *claudeSessionSlotRecorder) UnregisterSession(_ context.Context, id int64, _ string) error {
	r.released = id
	return nil
}

type claudeConcurrencyRecorder struct {
	mockConcurrencyCache
	released int64
}

func (r *claudeConcurrencyRecorder) ReleaseAccountSlot(_ context.Context, id int64, _ string) error {
	r.released = id
	return nil
}

func TestClaudeSessionLosingClaimReleasesBothSlots(t *testing.T) {
	f := newLoadAwareRestrictionFixture(t, false, nil, nil)
	repo := f.svc.accountRepo.(*mockAccountRepoForPlatform)
	for i := range repo.accounts {
		repo.accounts[i].Type = AccountTypeOAuth
		repo.accounts[i].Extra = map[string]any{"max_sessions": 1}
	}
	slots := &claudeSessionSlotRecorder{}
	concurrency := &claudeConcurrencyRecorder{}
	f.svc.sessionLimitCache = slots
	f.svc.concurrencyService = NewConcurrencyService(concurrency)
	f.svc.claudeSessionStore = &conflictingClaudeClaim{}
	result, err := f.svc.SelectAccountWithLoadAwareness(f.ctx, &f.groupID, uuid.NewString(), "claude-fable-5-1", nil, "", 0)
	require.Error(t, err)
	require.Nil(t, result)
	require.Positive(t, slots.registered)
	require.Equal(t, slots.registered, slots.released)
	require.Equal(t, slots.registered, concurrency.released)
}

func TestClaudeSessionGatewayOnlyResumeReachesAPIKeyUpstream(t *testing.T) {
	svc, up := newClaudeContractGateway(t)
	account := newClaude2292Account(AccountTypeAPIKey)
	session := uuid.NewString()
	_, err := svc.claudeSessionStore.ClaimClaudeSessionAccountID(context.Background(), session, account.ID)
	require.NoError(t, err)
	_, _, err = callClaudeContract(t, svc, account, []byte(`{"model":"claude-sonnet-4-6","max_tokens":8,"messages":[{"role":"user","content":"next turn"}]}`), map[string]string{claudeConversationHeader: session}, "/v1/messages")
	require.NoError(t, err)
	require.Equal(t, session, getHeaderRaw(up.request.Header, "X-Claude-Code-Session-Id"))
}

func TestClaudeSessionAdapterKeepsMetadataOnlySession(t *testing.T) {
	for _, kind := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		for _, route := range []string{"/v1/chat/completions", "/v1/responses"} {
			svc, up := newClaudeContractGateway(t)
			account := newClaude2292Account(kind)
			session := uuid.NewString()
			body := []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-6","max_tokens":8,"messages":[{"role":"user","content":"hello"}],"input":"hello","metadata":{"user_id":%q}}`, fmt.Sprintf(`{"device_id":"test","account_uuid":"","session_id":%q}`, session)))
			_, _, err := callClaudeContract(t, svc, account, body, nil, route)
			require.NoError(t, err, kind+route)
			require.Equal(t, session, getHeaderRaw(up.request.Header, "X-Claude-Code-Session-Id"), kind+route)
			owner, err := svc.claudeSessionStore.GetClaudeSessionAccountID(context.Background(), session)
			require.NoError(t, err)
			require.Equal(t, account.ID, owner)
		}
	}
}

func TestClaudeSessionRejectsConflictingWireIdentityAndUnknownResume(t *testing.T) {
	svc, up := newClaudeContractGateway(t)
	account := newClaude2292Account(AccountTypeAPIKey)
	first, second := uuid.NewString(), uuid.NewString()
	body := []byte(fmt.Sprintf(`{"metadata":{"user_id":%q}}`, fmt.Sprintf(`{"device_id":"test","account_uuid":"","session_id":%q}`, first)))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	// A differing outgoing identity is rejected even if a rewrite produced it.
	err := svc.bindClaudeConversation(context.Background(), c, account, body, http.Header{"X-Claude-Code-Session-Id": {second}})
	require.ErrorContains(t, err, "conflicting")
	require.Zero(t, up.calls)
	c, _ = gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	c.Request.Header["X-Claude-Code-Session-Id"] = []string{first, second}
	require.ErrorContains(t, svc.ValidateClaudeSessionRouting(context.Background(), c, nil), "conflicting")
	c, _ = gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	c.Request.Header.Set(claudeConversationHeader, first)
	require.ErrorContains(t, svc.ValidateClaudeSessionRouting(context.Background(), c, nil), "unknown")
	owner, err := svc.claudeSessionStore.GetClaudeSessionAccountID(context.Background(), first)
	require.NoError(t, err)
	require.Zero(t, owner)
}

func TestClaudeSessionWireIdentityAcrossTurnsAndEndpoints(t *testing.T) {
	svc, up := newClaudeContractGateway(t)
	account := newClaude2292Account(AccountTypeOAuth)
	first, second := uuid.NewString(), uuid.NewString()
	for _, session := range []string{first, second, first} {
		for route, body := range map[string][]byte{
			"/v1/messages":              []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"same opening"}]}`),
			"/v1/messages/count_tokens": []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"same opening"}]}`),
			"/v1/chat/completions":      []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"same opening"}]}`),
			"/v1/responses":             []byte(`{"model":"claude-sonnet-4-6","input":"same opening"}`),
		} {
			_, rec, err := callClaudeContract(t, svc, account, body, map[string]string{"X-Claude-Code-Session-Id": session}, route)
			require.NoError(t, err, route)
			require.Equal(t, session, rec.Header().Get(claudeConversationHeader))
			require.Equal(t, session, getHeaderRaw(up.request.Header, "X-Claude-Code-Session-Id"))
			metadata := ParseMetadataUserID(gjson.GetBytes(up.body, "metadata.user_id").String())
			if strings.HasSuffix(route, "/count_tokens") {
				require.Nil(t, metadata, "native count_tokens correlates through the session header")
			} else {
				require.NotNil(t, metadata, route)
				require.Equal(t, session, metadata.SessionID, route)
			}
			owner, err := svc.claudeSessionStore.GetClaudeSessionAccountID(context.Background(), session)
			require.NoError(t, err)
			require.Equal(t, account.ID, owner)
		}
	}
}

func TestClaudeSessionStoreFailurePreventsSend(t *testing.T) {
	for _, store := range []ClaudeSessionStore{nil, &memoryClaudeSessionStore{err: errors.New("database offline")}} {
		svc, up := newClaudeContractGateway(t)
		svc.claudeSessionStore = store
		_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}]}`), nil, "/v1/messages")
		require.Error(t, err)
		require.Equal(t, 503, rec.Code)
		require.Zero(t, up.calls)
	}
}

func TestClaudeSessionConcurrentFirstRequestsCannotSplit(t *testing.T) {
	store := &memoryClaudeSessionStore{}
	session := uuid.NewString()
	const workers = 24
	services := make([]*GatewayService, workers)
	upstreams := make([]*claudeContractUpstream, workers)
	for i := range services {
		services[i], upstreams[i] = newClaudeContractGateway(t)
		services[i].claudeSessionStore = store
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range services {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			account := newClaude2292Account(AccountTypeOAuth)
			account.ID = int64(i + 1)
			_, _, _ = callClaudeContract(t, services[i], account, []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}]}`), map[string]string{"X-Claude-Code-Session-Id": session}, "/v1/messages")
		}(i)
	}
	close(start)
	wg.Wait()
	winner, err := store.GetClaudeSessionAccountID(context.Background(), session)
	require.NoError(t, err)
	calls := 0
	for i, up := range upstreams {
		calls += up.calls
		if up.calls > 0 {
			require.Equal(t, winner, int64(i+1))
		}
	}
	require.Equal(t, 1, calls)
}

func TestClaudeSessionNativeAndAdaptersShareOwner(t *testing.T) {
	svc, up := newClaudeContractGateway(t)
	session := uuid.NewString()
	account := newClaude2292Account(AccountTypeAPIKey)
	// An empty provider UUID avoids using account-UUID validation as a substitute
	// for the actual conversation ownership guard.
	native := []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-6","max_tokens":8,"messages":[{"role":"user","content":"hello"}],"metadata":{"user_id":"user_%s_account__session_%s"}}`, "abcdef", session))
	headers := map[string]string{"User-Agent": "claude-cli/2.1.292 (external, cli)", "X-Claude-Code-Session-Id": session}
	_, _, err := callClaudeContract(t, svc, account, native, headers, "/v1/messages")
	require.NoError(t, err)
	_, _, err = callClaudeContract(t, svc, account, native, headers, "/v1/messages/count_tokens")
	require.NoError(t, err)
	before := up.calls
	other := newClaude2292Account(AccountTypeOAuth)
	other.ID++
	for route, body := range map[string][]byte{
		"/v1/messages":              native,
		"/v1/messages/count_tokens": native,
		"/v1/chat/completions":      []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}]}`),
		"/v1/responses":             []byte(`{"model":"claude-sonnet-4-6","input":"hello"}`),
	} {
		_, rec, err := callClaudeContract(t, svc, other, body, headers, route)
		require.Error(t, err, route)
		require.Equal(t, 503, rec.Code, route)
		require.Equal(t, before, up.calls, route)
	}
}

func TestClaudeSessionSchedulingSurvivesCacheLossAndUnavailability(t *testing.T) {
	for _, loadAware := range []bool{false, true} {
		t.Run(fmt.Sprint(loadAware), func(t *testing.T) {
			owner := Account{ID: 1, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Priority: 100, Concurrency: 5}
			alternative := Account{ID: 2, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 5}
			repo := &mockAccountRepoForPlatform{accounts: []Account{owner, alternative}, accountsByID: map[int64]*Account{1: &owner, 2: &alternative}}
			session := uuid.NewString()
			store := &memoryClaudeSessionStore{owners: map[string]int64{session: 1}}
			svc := &GatewayService{accountRepo: repo, cfg: testConfig(), claudeSessionStore: store}
			if loadAware {
				svc.cfg.Gateway.Scheduling.LoadBatchEnabled = true
				svc.concurrencyService = NewConcurrencyService(&mockConcurrencyCache{})
			}
			choose := func(key string) (*Account, error) {
				if loadAware {
					r, err := svc.SelectAccountWithLoadAwareness(context.Background(), nil, key, "claude-sonnet-4-6", nil, "", 0)
					if err != nil {
						return nil, err
					}
					if r.ReleaseFunc != nil {
						r.ReleaseFunc()
					}
					return r.Account, nil
				}
				return svc.SelectAccountForModel(context.Background(), nil, key, "claude-sonnet-4-6")
			}
			for _, key := range []string{session, claudeConversationRoutingKey(1, session), claudeConversationRoutingKey(999, session)} {
				acc, err := choose(key)
				require.NoError(t, err)
				require.Equal(t, int64(1), acc.ID)
			}
			repo.accounts = []Account{alternative}
			owner.Schedulable = false
			_, err := choose(session)
			require.Error(t, err)
			bound, err := store.GetClaudeSessionAccountID(context.Background(), session)
			require.NoError(t, err)
			require.Equal(t, int64(1), bound)
		})
	}
}
