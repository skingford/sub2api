package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"time"
)

var (
	ErrRecoveryBusy      = errors.New("conversation has another active request; retry later")
	ErrRecoveryUncertain = errors.New("previous operation has an uncertain outcome; automatic replay is stopped; start a new conversation after checking tool results")
	ErrRecoveryConflict  = errors.New("conversation history or identity conflicts with its recovery record")
	ErrRecoveryExpired   = errors.New("conversation recovery material expired; start a new conversation")
)

type RecoveryScope struct {
	UserID        int64
	GroupID       int64
	ClientSession string
}
type RecoveryRow struct {
	Scope                                                         RecoveryScope
	ID, Route, Model, Session, Lease, Operation, State, Principal string
	Generation, AccountID, SourceVersion, CheckpointVersion       int64
	Material, Source, Checkpoint, Restore, Replay                 []byte
	MigrationBlocked                                              bool
}
type RecoveryAcquire struct {
	Scope                             RecoveryScope
	Route, Model, Digest, Idempotency string
	ReadOnly                          bool
	Lease, Retention                  time.Duration
}
type RecoveryFinish struct {
	Row                        RecoveryRow
	State                      string
	Material, Source, Result   []byte
	BlockMigration             bool
	Retention                  time.Duration
	ResetHistory, ClearRestore bool
}
type RecoverySummaryJob struct {
	Row   RecoveryRow
	Token string
}
type RecoverySummaryUsage struct {
	InputTokens, OutputTokens int64
	Confirmed                 bool
}

// Every operation takes authenticated scope AND immutable row/segment identity.
// A UUID or cache key supplied by a client is never sufficient to access data.
type ClaudeRecoveryStore interface {
	AcquireRecovery(context.Context, RecoveryAcquire) (*RecoveryRow, error)
	BindRecoveryAccount(context.Context, RecoveryRow, int64, string, bool) error
	MarkRecoverySent(context.Context, RecoveryRow) error
	RenewRecoveryLease(context.Context, RecoveryRow, time.Duration) error
	FinishRecovery(context.Context, RecoveryFinish) error
	MigrateRecovery(context.Context, RecoveryRow, string, int64, string, []byte, string) (*RecoveryRow, error)
	CheckRecoveryVersion(context.Context, RecoveryRow) error
	ClaimRecoverySummary(context.Context, time.Duration, []int64, int) (*RecoverySummaryJob, error)
	SaveRecoverySummary(context.Context, RecoverySummaryJob, []byte, bool) error
	ReserveRecoverySummaryBudget(context.Context, int64, int) error
	RecordRecoverySummaryUsage(context.Context, int64, RecoverySummaryUsage) error
	PurgeRecoveryMaterial(context.Context) error
	IsRecoveryUpstreamSession(context.Context, string) (bool, error)
	HasRecoveryConversation(context.Context, RecoveryScope) (bool, error)
}

type RecoveryHistory struct {
	Compaction  *RecoveryCompaction `json:"compaction,omitempty"`
	SummaryBase *RecoveryCheckpoint `json:"-"`
	Route       string              `json:"route"`
	Model       string              `json:"model"`
	ConfigHash  string              `json:"config_hash"`
	Messages    []json.RawMessage   `json:"messages"`
}
type RecoveryCheckpoint struct {
	Attachments  []RecoveryAttachment `json:"attachments"`
	Critical     []RecoveryFact       `json:"critical"`
	PrefixHashes []string             `json:"prefix_hashes"`
	ConfigHash   string               `json:"config_hash"`
	Summary      string               `json:"summary"`
	Facts        []RecoveryFact       `json:"facts"`
	Recent       []json.RawMessage    `json:"recent"`
}
type RecoveryAttachment struct {
	Index int             `json:"index"`
	Block json.RawMessage `json:"block"`
}

type RecoveryFact struct {
	Index int    `json:"index"`
	Quote string `json:"quote"`
}
type RecoveryReplay struct {
	Status  int         `json:"status"`
	Headers http.Header `json:"headers"`
	Body    []byte      `json:"body"`
}

type claudeRecoveryContextKey struct{}
type ClaudeRecoveryExchange struct {
	compactionBase             *RecoveryHistory
	compactionPrefix           int
	resetHistory, clearRestore bool
	Row                        RecoveryRow
	History                    RecoveryHistory
	Body                       []byte
	Replay                     *RecoveryReplay
	ReadOnly                   bool
	Sent                       atomic.Bool
	ResponseStatus             atomic.Int64
	LeaseLost                  atomic.Bool
	manager                    *ClaudeRecoveryService
	cancel                     context.CancelFunc
}

func ClaudeRecoveryFromContext(ctx context.Context) *ClaudeRecoveryExchange {
	if ctx == nil {
		return nil
	}
	p, _ := ctx.Value(claudeRecoveryContextKey{}).(*ClaudeRecoveryExchange)
	return p
}
func (p *ClaudeRecoveryExchange) Context(ctx context.Context) context.Context {
	return context.WithValue(ctx, claudeRecoveryContextKey{}, p)
}
func (p *ClaudeRecoveryExchange) Close() {
	if p.cancel != nil {
		p.cancel()
	}
}
