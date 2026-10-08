package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type ClaudeRecoveryService struct {
	cfg        config.ClaudeRecoveryConfig
	store      ClaudeRecoveryStore
	cipher     recoveryCipher
	summarizer ClaudeRecoverySummarizer
	initErr    error
	once       sync.Once
	workerCtx  context.Context
	stop       context.CancelFunc
}

func NewClaudeRecoveryService(cfg config.ClaudeRecoveryConfig, store ClaudeRecoveryStore, overrides ...ClaudeRecoverySummarizer) *ClaudeRecoveryService {
	m := &ClaudeRecoveryService{cfg: cfg.WithDefaults(), store: store}
	m.workerCtx, m.stop = context.WithCancel(context.Background())
	if cfg.Enabled {
		m.initErr = m.cfg.Validate()
		if m.initErr == nil {
			m.cipher, m.initErr = newRecoveryCipher(m.cfg.EncryptionKey)
		}
		m.summarizer = newRecoveryHTTPSummarizer(m.cfg)
		if len(overrides) > 0 && overrides[0] != nil {
			m.summarizer = overrides[0]
		}
	}
	return m
}
func (m *ClaudeRecoveryService) Start() {
	if m != nil && m.store != nil {
		m.once.Do(func() { go m.worker() })
	}
}
func (m *ClaudeRecoveryService) Close() { m.stop() }
func (m *ClaudeRecoveryService) Allowed(group int64) bool {
	if m == nil || !m.cfg.Enabled {
		return false
	}
	for _, id := range m.cfg.GroupIDs {
		if id == group {
			return true
		}
	}
	return false
}
func (s *GatewayService) ClaudeRecovery() *ClaudeRecoveryService { return s.claudeRecovery }
func (m *ClaudeRecoveryService) Existing(ctx context.Context, scope RecoveryScope) (bool, error) {
	if m == nil || m.store == nil {
		return false, nil
	}
	return m.store.HasRecoveryConversation(ctx, scope)
}

func recoveryAccountPrincipal(a *Account) string {
	if a == nil {
		return ""
	}
	identity := a.GetExtraString("account_uuid")
	if a.Type == AccountTypeAPIKey {
		identity = a.GetCredential("api_key")
	}
	return recoveryHash([]byte(a.Platform + ":" + a.Type + ":" + identity))
}
func recoveryAccountSupported(a *Account) bool {
	return a != nil && a.Platform == PlatformAnthropic && (a.Type == AccountTypeOAuth || a.Type == AccountTypeAPIKey)
}
func recoveryAccountUnavailable(a *Account, err error) bool {
	if errors.Is(err, ErrAccountNotFound) {
		return true
	}
	if err != nil || a == nil {
		return false
	}
	// HTTP authentication, permission, policy errors, cooldowns and rate limits
	// are deliberately not interpreted as permission to switch credentials.
	return a.Status == "disabled" || (a.Status == StatusActive && !a.Schedulable)
}

func (m *ClaudeRecoveryService) Begin(ctx context.Context, g *GatewayService, scope RecoveryScope, body []byte, route, key string, readOnly bool) (p *ClaudeRecoveryExchange, err error) {
	if !m.Allowed(scope.GroupID) || scope.UserID <= 0 {
		return nil, ErrRecoveryConflict
	}
	if m.initErr != nil || m.store == nil {
		return nil, fmt.Errorf("managed recovery is not correctly configured")
	}
	if claude.EffectiveCLIVersion() != claudeCompatibilityVersion {
		return nil, fmt.Errorf("managed recovery requires the verified 2.1.292 profile")
	}
	if len(key) > 128 {
		return nil, fmt.Errorf("idempotency key is too long")
	}
	h, e := parseRecoveryHistory(body, route, m.cfg.MaxHistoryBytes)
	if e != nil {
		return nil, e
	}
	if !readOnly && !recoveryClosedTools(h) {
		return nil, fmt.Errorf("unresolved or foreign tool results cannot be forwarded in managed mode: %w", ErrRecoveryConflict)
	}
	storedKey := ""
	if key != "" {
		storedKey = recoveryHash([]byte(key))
	}
	row, e := m.store.AcquireRecovery(ctx, RecoveryAcquire{Scope: scope, Route: route, Model: h.Model, Digest: recoveryHash(body), Idempotency: storedKey, ReadOnly: readOnly, Lease: time.Duration(m.cfg.LeaseSeconds) * time.Second, Retention: time.Duration(m.cfg.RetentionHours) * time.Hour})
	if e != nil {
		return nil, e
	}
	p = &ClaudeRecoveryExchange{Row: *row, History: h, Body: body, ReadOnly: readOnly, manager: m}
	if len(row.Replay) > 0 {
		var replay RecoveryReplay
		if e = m.cipher.open(*row, "replay:"+key, row.Replay, &replay); e != nil {
			return nil, e
		}
		p.Replay = &replay
		return p, nil
	}
	// Any pre-send failure releases the lease without changing the existing owner.
	defer func() {
		if err != nil && !readOnly {
			_ = m.store.FinishRecovery(context.WithoutCancel(ctx), RecoveryFinish{Row: p.Row, State: "failed", Retention: time.Duration(m.cfg.RetentionHours) * time.Hour})
			p.Close()
		}
	}()
	if len(row.Material) > 0 {
		var old RecoveryHistory
		if e = m.cipher.open(*row, "history", row.Material, &old); e != nil {
			return p, e
		}
		if readOnly {
			known := map[string]bool{}
			for _, msg := range old.Messages {
				for _, hash := range recoveryOpaqueHashes(msg) {
					known[hash] = true
				}
			}
			for _, msg := range h.Messages {
				for _, hash := range recoveryOpaqueHashes(msg) {
					if !known[hash] {
						return p, ErrRecoveryConflict
					}
				}
			}
		}
		if !readOnly && !recoveryHistoryExtends(old, h) {
			return p, fmt.Errorf("history rewind, compaction or branch does not match the stored conversation: %w", ErrRecoveryConflict)
		}
	} else if recoveryHasOpaqueHistory(h) {
		return p, fmt.Errorf("a new managed conversation cannot import opaque signed history")
	}
	if !readOnly {
		leaseCtx, cancel := context.WithCancel(ctx)
		p.cancel = cancel
		leaseRow := p.Row
		go func() {
			ticker := time.NewTicker(time.Duration(m.cfg.LeaseSeconds) * time.Second / 3)
			defer ticker.Stop()
			for {
				select {
				case <-leaseCtx.Done():
					if ctx.Err() != nil {
						p.LeaseLost.Store(true)
					}
					return
				case <-ticker.C:
					renewCtx, stop := context.WithTimeout(leaseCtx, 5*time.Second)
					e := m.store.RenewRecoveryLease(renewCtx, leaseRow, time.Duration(m.cfg.LeaseSeconds)*time.Second)
					stop()
					if e != nil {
						p.LeaseLost.Store(true)
						return
					}
				}
			}
		}()
	}
	if row.AccountID > 0 {
		account, lookupErr := g.accountRepo.GetByID(ctx, row.AccountID)
		if recoveryAccountUnavailable(account, lookupErr) {
			if readOnly || row.MigrationBlocked || !recoverySafeBoundary(h) {
				return p, fmt.Errorf("bound account is unavailable outside an automatic recovery boundary")
			}
			cp, e := m.checkpointForMigration(ctx, *row, h)
			if e != nil {
				return p, e
			}
			group := scope.GroupID
			candidate, e := g.SelectAccountForModelWithExclusions(p.Context(ctx), &group, "", h.Model, map[int64]struct{}{row.AccountID: {}})
			if e != nil {
				return p, e
			}
			if !recoveryAccountSupported(candidate) || !recoveryAccountInScope(candidate, scope) || !recoveryAccountModelCompatible(candidate, h.Model) {
				return p, ErrRecoveryConflict
			}
			next := uuid.NewString()
			var encrypted []byte
			if len(cp.PrefixHashes) > 0 {
				encrypted, e = m.cipher.seal(*row, "restore:"+next, cp)
				if e != nil {
					return p, e
				}
			}
			moved, e := m.store.MigrateRecovery(ctx, *row, next, candidate.ID, recoveryAccountPrincipal(candidate), encrypted, "bound_account_disabled_or_removed")
			if e != nil {
				return p, e
			}
			p.Row = *moved
		} else if lookupErr != nil {
			return p, lookupErr
		} else if !recoveryAccountSupported(account) || recoveryAccountPrincipal(account) != row.Principal {
			return p, fmt.Errorf("upstream account identity changed; start a new conversation")
		}
	}
	if len(p.Row.Restore) > 0 {
		var cp RecoveryCheckpoint
		if e = m.cipher.open(p.Row, "restore:"+p.Row.Session, p.Row.Restore, &cp); e != nil {
			return p, e
		}
		p.Body, e = recoveryApplyCheckpoint(body, h, &cp)
		if e != nil {
			return p, e
		}
	}
	p.Body, e = sjson.DeleteBytes(p.Body, "diagnostics")
	if e != nil {
		return p, e
	}
	if len(p.Row.Restore) > 0 {
		p.Body, e = recoveryRewriteAttribution(p.Body)
		if e != nil {
			return p, e
		}
	}
	p.Body, e = recoveryRewriteSession(p.Body, p.Row.Session, "")
	if e != nil {
		return p, e
	}
	m.Start()
	return p, nil
}

func (m *ClaudeRecoveryService) checkpointForMigration(ctx context.Context, row RecoveryRow, h RecoveryHistory) (RecoveryCheckpoint, error) {
	var cp RecoveryCheckpoint
	if len(h.Messages) == 1 && recoverySafeBoundary(h) {
		return cp, nil
	}
	if len(row.Checkpoint) > 0 {
		if e := m.cipher.open(row, "checkpoint", row.Checkpoint, &cp); e != nil {
			return cp, e
		}
		if cp.ConfigHash != h.ConfigHash || len(cp.PrefixHashes) > len(h.Messages)-1 {
			return cp, ErrRecoveryConflict
		}
		for i, hash := range cp.PrefixHashes {
			if recoveryMessageHash(h.Messages[i], h.Route) != hash {
				return cp, ErrRecoveryConflict
			}
		}
		// Preserve the entire unsummarized delta as quoted observations. Never drop
		// recent actions just because a background checkpoint is one turn behind.
		for i := len(cp.PrefixHashes); i < len(h.Messages)-1; i++ {
			text, e := recoveryVisibleText(h.Messages[i])
			if e != nil {
				return cp, e
			}
			raw, _ := json.Marshal(map[string]any{"source_index": i, "observation": text})
			cp.Recent = append(cp.Recent, raw)
			cp.PrefixHashes = append(cp.PrefixHashes, recoveryMessageHash(h.Messages[i], h.Route))
		}
		cp.Critical = recoveryCriticalInstructions(h.Messages[:len(h.Messages)-1])
		var attachmentErr error
		cp.Attachments, attachmentErr = recoveryAttachments(h.Messages[:len(h.Messages)-1])
		if attachmentErr != nil {
			return cp, attachmentErr
		}
		raw, e := json.Marshal(cp)
		if e != nil {
			return cp, e
		}
		if len(raw) <= m.cfg.MaxHistoryBytes/2 && len(cp.PrefixHashes) > 0 {
			return cp, nil
		}
	}
	return m.summarize(ctx, row, h)
}
func (m *ClaudeRecoveryService) summarize(ctx context.Context, row RecoveryRow, h RecoveryHistory) (RecoveryCheckpoint, error) {
	if e := m.store.ReserveRecoverySummaryBudget(ctx, row.Scope.UserID, m.cfg.MaxSummaryCallsPerUserDay); e != nil {
		return RecoveryCheckpoint{}, fmt.Errorf("summary budget or storage is unavailable")
	}
	summaryCtx, cancel := context.WithTimeout(ctx, time.Duration(m.cfg.SummaryTimeoutSeconds)*time.Second)
	defer cancel()
	if len(row.Checkpoint) > 0 {
		var base RecoveryCheckpoint
		if e := m.cipher.open(row, "checkpoint", row.Checkpoint, &base); e != nil {
			return RecoveryCheckpoint{}, e
		}
		if base.ConfigHash != h.ConfigHash || len(base.PrefixHashes) > len(h.Messages)-1 {
			return RecoveryCheckpoint{}, ErrRecoveryConflict
		}
		for i, hash := range base.PrefixHashes {
			if recoveryMessageHash(h.Messages[i], h.Route) != hash {
				return RecoveryCheckpoint{}, ErrRecoveryConflict
			}
		}
		h.SummaryBase = &base
	}
	cp, usage, e := m.summarizer.Summarize(summaryCtx, h)
	if e == nil {
		if cp.ConfigHash != h.ConfigHash || len(cp.PrefixHashes) != len(h.Messages)-1 {
			e = ErrRecoveryConflict
		}
		for i, hash := range cp.PrefixHashes {
			if i >= len(h.Messages) || hash != recoveryMessageHash(h.Messages[i], h.Route) {
				e = ErrRecoveryConflict
				break
			}
		}
		cp.Critical = recoveryCriticalInstructions(h.Messages[:len(h.Messages)-1])
		var attachErr error
		cp.Attachments, attachErr = recoveryAttachments(h.Messages[:len(h.Messages)-1])
		if attachErr != nil {
			e = attachErr
		}
	}
	accountingCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	if accountingErr := m.store.RecordRecoverySummaryUsage(accountingCtx, row.Scope.UserID, usage); accountingErr != nil {
		return RecoveryCheckpoint{}, accountingErr
	}
	return cp, e
}
func (m *ClaudeRecoveryService) worker() {
	nextPurge := time.Time{}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.workerCtx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(m.workerCtx, time.Duration(m.cfg.SummaryTimeoutSeconds+15)*time.Second)
			if time.Now().After(nextPurge) {
				_ = m.store.PurgeRecoveryMaterial(ctx)
				nextPurge = time.Now().Add(time.Minute)
			}
			if !m.cfg.Enabled || m.initErr != nil {
				cancel()
				continue
			}
			job, e := m.store.ClaimRecoverySummary(ctx, time.Duration(m.cfg.SummaryTimeoutSeconds+10)*time.Second, m.cfg.GroupIDs, m.cfg.CheckpointEveryTurns)
			if e == nil && job != nil {
				var h RecoveryHistory
				e = m.cipher.open(job.Row, "source", job.Row.Source, &h)
				var payload []byte
				if e == nil {
					var cp RecoveryCheckpoint
					cp, e = m.summarize(ctx, job.Row, h)
					if e == nil {
						payload, e = m.cipher.seal(job.Row, "checkpoint", cp)
					}
				}
				_ = m.store.SaveRecoverySummary(ctx, *job, payload, e == nil)
			}
			cancel()
		}
	}
}

func recoveryRewriteSession(body []byte, session, account string) ([]byte, error) {
	uid := ParseMetadataUserID(gjson.GetBytes(body, "metadata.user_id").String())
	if uid == nil {
		return body, nil
	}
	uid.SessionID = session
	if account == "-" {
		uid.AccountUUID = ""
	} else if account != "" {
		uid.AccountUUID = account
	}
	version := "2.1.292"
	if !uid.IsNewFormat {
		version = "2.1.0"
	}
	return sjson.SetBytes(body, "metadata.user_id", FormatMetadataUserID(uid.DeviceID, uid.AccountUUID, uid.SessionID, version))
}
func (p *ClaudeRecoveryExchange) BeforeSend(ctx context.Context, a *Account) error {
	if p.LeaseLost.Load() || !recoveryAccountSupported(a) || !recoveryAccountInScope(a, p.Row.Scope) || !recoveryAccountModelCompatible(a, p.History.Model) {
		return ErrRecoveryConflict
	}
	if e := p.manager.store.BindRecoveryAccount(ctx, p.Row, a.ID, recoveryAccountPrincipal(a), p.ReadOnly); e != nil {
		return e
	}
	if !p.ReadOnly {
		if p.Sent.Load() {
			return fmt.Errorf("managed recovery does not replay an upstream attempt")
		}
		if e := p.manager.store.MarkRecoverySent(ctx, p.Row); e != nil {
			return e
		}
		p.Sent.Store(true)
	}
	return nil
}
func (p *ClaudeRecoveryExchange) Finish(ctx context.Context, status int, headers http.Header, body []byte, complete bool, key string) error {
	defer p.Close()
	if p.Replay != nil {
		return nil
	}
	if p.ReadOnly {
		return p.manager.store.CheckRecoveryVersion(ctx, p.Row)
	}
	state := "failed"
	upstream := int(p.ResponseStatus.Load())
	if p.Sent.Load() {
		state = "uncertain"
		if complete && status >= 200 && status < 300 && upstream >= 200 && upstream < 300 && !p.LeaseLost.Load() {
			state = "completed"
		}
		if upstream == 400 || upstream == 401 || upstream == 403 || upstream == 404 || upstream == 413 || upstream == 422 || upstream == 429 || upstream == 529 {
			state = "failed"
		}
	}
	f := RecoveryFinish{Row: p.Row, State: state, BlockMigration: upstream == 403, Retention: time.Duration(p.manager.cfg.RetentionHours) * time.Hour}
	var e error
	if state == "completed" {
		confirmed, parseErr := recoveryCompletedHistory(p.History, body, gjson.GetBytes(p.Body, "stream").Bool())
		if parseErr != nil {
			f.State = "uncertain"
			return p.manager.store.FinishRecovery(ctx, f)
		}
		if raw, marshalErr := json.Marshal(confirmed); marshalErr != nil || len(raw) > p.manager.cfg.MaxHistoryBytes {
			f.State = "uncertain"
			return p.manager.store.FinishRecovery(ctx, f)
		}
		f.Material, e = p.manager.cipher.seal(p.Row, "history", confirmed)
		if e != nil {
			return e
		}
		source := confirmed
		source.Messages = append(append([]json.RawMessage(nil), confirmed.Messages...), json.RawMessage(`{"role":"user","content":""}`))
		if recoverySafeBoundary(source) && len(source.Messages) > 1 {
			f.Source, e = p.manager.cipher.seal(p.Row, "source", source)
			if e != nil {
				return e
			}
		}
		if key != "" && len(body) > 0 {
			f.Result, e = p.manager.cipher.seal(p.Row, "replay:"+key, RecoveryReplay{Status: status, Headers: headers, Body: body})
			if e != nil {
				return e
			}
		}
	}
	return p.manager.store.FinishRecovery(ctx, f)
}

// In managed mode incoming identity has already been authenticated/resolved by
// the handler. Outgoing identity is checked against the immutable segment.
func applyClaudeRecoveryIdentity(ctx context.Context, a *Account, body []byte) ([]byte, error) {
	p := ClaudeRecoveryFromContext(ctx)
	if p == nil {
		return body, nil
	}
	account := a.GetExtraString("account_uuid")
	if account == "" {
		account = "-"
	}
	return recoveryRewriteSession(body, p.Row.Session, account)
}
func (s *GatewayService) doClaudeHTTP(req *http.Request, proxy string, a *Account, profile *tlsfingerprint.Profile) (*http.Response, error) {
	if p := ClaudeRecoveryFromContext(req.Context()); p != nil {
		if rejection, e := s.recoveryPreflightCount(req, proxy, a, profile, p); e != nil {
			return nil, e
		} else if rejection != nil {
			return rejection, nil
		}
		if e := p.BeforeSend(req.Context(), a); e != nil {
			return nil, e
		}
	}
	resp, e := s.httpUpstream.DoWithTLS(req, proxy, a.ID, a.Concurrency, profile)
	if p := ClaudeRecoveryFromContext(req.Context()); p != nil && resp != nil {
		p.ResponseStatus.Store(int64(resp.StatusCode))
	}
	return resp, e
}

func recoveryAccountInScope(a *Account, scope RecoveryScope) bool {
	if a == nil {
		return false
	}
	for _, group := range a.AccountGroups {
		if group.GroupID == scope.GroupID {
			return true
		}
	}
	for _, group := range a.GroupIDs {
		if group == scope.GroupID {
			return true
		}
	}
	return false
}

func recoveryAccountModelCompatible(a *Account, model string) bool {
	if !recoveryAccountSupported(a) {
		return false
	}
	if value, ok := a.Extra["claude_native_passthrough"].(bool); ok && !value {
		return false
	}
	if value, ok := a.Extra["session_id_masking_enabled"].(bool); ok && value {
		return false
	}
	return a.Type != AccountTypeAPIKey || a.GetMappedModel(model) == model
}
