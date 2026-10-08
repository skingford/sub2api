package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

var _ service.ClaudeRecoveryStore = (*accountRepository)(nil)

const recoveryColumns = `c.id::text,c.route,c.model,c.active_session_id::text,c.generation,c.state,
 COALESCE(c.lease_token::text,''),COALESCE(c.last_operation::text,''),c.material,c.source,c.source_version,
 c.checkpoint,c.checkpoint_version,c.migration_blocked,COALESCE(s.account_id,0),COALESCE(s.principal_hash,''),s.restore`
const recoveryFrom = ` FROM claude_recovery_conversations c LEFT JOIN claude_recovery_segments s ON s.session_id=c.active_session_id AND s.conversation_id=c.id `
const recoveryScopeSQL = `c.user_id=$1 AND c.group_id=$2 AND c.client_session_id=$3::uuid`

func recoveryScopeArgs(scope service.RecoveryScope) []any {
	return []any{scope.UserID, scope.GroupID, scope.ClientSession}
}
func validRecoveryScope(scope service.RecoveryScope) bool {
	u, e := uuid.Parse(scope.ClientSession)
	return e == nil && u != uuid.Nil && u.String() == scope.ClientSession && scope.UserID > 0 && scope.GroupID > 0
}
func readRecovery(ctx context.Context, q sqlExecutor, scope service.RecoveryScope, lock bool) (*service.RecoveryRow, error) {
	if !validRecoveryScope(scope) {
		return nil, service.ErrRecoveryConflict
	}
	query := "SELECT " + recoveryColumns + recoveryFrom + " WHERE " + recoveryScopeSQL
	if lock {
		query += " FOR UPDATE OF c"
	}
	rows, err := q.QueryContext(ctx, query, recoveryScopeArgs(scope)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, sql.ErrNoRows
	}
	p := &service.RecoveryRow{Scope: scope}
	err = rows.Scan(&p.ID, &p.Route, &p.Model, &p.Session, &p.Generation, &p.State, &p.Lease, &p.Operation, &p.Material, &p.Source, &p.SourceVersion, &p.Checkpoint, &p.CheckpointVersion, &p.MigrationBlocked, &p.AccountID, &p.Principal, &p.Restore)
	return p, err
}
func (r *accountRepository) recoveryTx(ctx context.Context) (*sql.Tx, error) {
	b, ok := r.sql.(interface {
		BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
	})
	if !ok {
		return nil, fmt.Errorf("recovery requires transactional PostgreSQL storage")
	}
	return b.BeginTx(ctx, nil)
}
func recoveryAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return service.ErrRecoveryConflict
	}
	return nil
}

func (r *accountRepository) AcquireRecovery(ctx context.Context, a service.RecoveryAcquire) (*service.RecoveryRow, error) {
	if !validRecoveryScope(a.Scope) {
		return nil, service.ErrRecoveryConflict
	}
	tx, err := r.recoveryTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	p, err := readRecovery(ctx, tx, a.Scope, true)
	if errors.Is(err, sql.ErrNoRows) {
		// Existing strict sessions cannot be silently adopted, and an upstream UUID
		// must never be treated as another client's logical conversation identifier.
		var exists bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM claude_session_ownership WHERE session_id=$1) OR EXISTS(SELECT 1 FROM claude_recovery_segments WHERE session_id=$1)`, a.Scope.ClientSession).Scan(&exists)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, service.ErrRecoveryConflict
		}
		id, sid := uuid.NewString(), uuid.NewString()
		result, e := tx.ExecContext(ctx, `INSERT INTO claude_recovery_conversations(id,user_id,group_id,client_session_id,route,model,active_session_id,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,NOW()+$8*INTERVAL '1 second') ON CONFLICT(user_id,group_id,client_session_id) DO NOTHING`, id, a.Scope.UserID, a.Scope.GroupID, a.Scope.ClientSession, a.Route, a.Model, sid, a.Retention.Seconds())
		if e != nil {
			return nil, e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return nil, e
		}
		if n == 1 {
			if _, e = tx.ExecContext(ctx, `INSERT INTO claude_recovery_segments(session_id,conversation_id,generation) VALUES($1,$2,1)`, sid, id); e != nil {
				return nil, e
			}
		}
		p, err = readRecovery(ctx, tx, a.Scope, true)
	}
	if err != nil {
		return nil, err
	}
	if p.Route != a.Route || p.Model != a.Model {
		return nil, service.ErrRecoveryConflict
	}
	var expired, leaseExpired bool
	if err = tx.QueryRowContext(ctx, `SELECT expires_at<=NOW(),COALESCE(lease_until<=NOW(),TRUE) FROM claude_recovery_conversations WHERE id=$1`, p.ID).Scan(&expired, &leaseExpired); err != nil {
		return nil, err
	}
	if expired || p.State == "expired" {
		return nil, service.ErrRecoveryExpired
	}
	if p.State == "uncertain" {
		return nil, service.ErrRecoveryUncertain
	}
	if p.State == "busy" {
		if !leaseExpired {
			return nil, service.ErrRecoveryBusy
		}
		var state string
		if err = tx.QueryRowContext(ctx, `SELECT state FROM claude_recovery_operations WHERE id=$1 AND conversation_id=$2`, p.Operation, p.ID).Scan(&state); err != nil {
			return nil, err
		}
		if state == "sent" {
			if _, err = tx.ExecContext(ctx, `UPDATE claude_recovery_conversations SET state='uncertain',lease_token=NULL WHERE id=$1;`, p.ID); err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE claude_recovery_operations SET state='uncertain' WHERE id=$1`, p.Operation); err != nil {
				return nil, err
			}
			if err = tx.Commit(); err != nil {
				return nil, err
			}
			return nil, service.ErrRecoveryUncertain
		}
		if _, err = tx.ExecContext(ctx, `UPDATE claude_recovery_operations SET state='failed' WHERE id=$1`, p.Operation); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE claude_recovery_conversations SET state='ready',lease_token=NULL,lease_until=NULL WHERE id=$1`, p.ID); err != nil {
			return nil, err
		}
		p.State = "ready"
		p.Lease = ""

	}
	if a.ReadOnly {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return p, nil
	}
	if a.Idempotency != "" {
		var digest, state string
		var replay []byte
		err = tx.QueryRowContext(ctx, `SELECT request_digest,state,result FROM claude_recovery_operations WHERE conversation_id=$1 AND idempotency_key=$2 `, p.ID, a.Idempotency).Scan(&digest, &state, &replay)
		if err == nil {
			if digest != a.Digest {
				return nil, service.ErrRecoveryConflict
			}
			if state == "completed" && len(replay) > 0 {
				p.Replay = replay
				if err = tx.Commit(); err != nil {
					return nil, err
				}
				return p, nil
			}
			if state == "failed" {
				// Preserve the failed attempt and its key hash while permitting
				// an explicit retry of a conclusively rejected/unsent operation.
				if _, e := tx.ExecContext(ctx, `UPDATE claude_recovery_operations SET idempotency_key=NULL WHERE conversation_id=$1 AND idempotency_key=$2 AND state='failed'`, p.ID, a.Idempotency); e != nil {
					return nil, e
				}
			} else {
				return nil, service.ErrRecoveryUncertain
			}
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	p.Lease, p.Operation = uuid.NewString(), uuid.NewString()
	_, err = tx.ExecContext(ctx, `INSERT INTO claude_recovery_operations(id,conversation_id,session_id,idempotency_key,idempotency_hash,request_digest,expires_at) VALUES($1,$2,$3,NULLIF($4,''),NULLIF($4,''),$5,NOW()+$6*INTERVAL '1 second')`, p.Operation, p.ID, p.Session, a.Idempotency, a.Digest, a.Retention.Seconds())
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE claude_recovery_conversations SET state='busy',lease_token=$2,lease_until=NOW()+$3*INTERVAL '1 second',last_operation=$4 WHERE id=$1`, p.ID, p.Lease, a.Lease.Seconds(), p.Operation)
	if err != nil {
		return nil, err
	}
	p.State = "busy"
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return p, nil
}

func requireRecoveryRow(current *service.RecoveryRow, expected service.RecoveryRow, readOnly bool) error {
	if current.ID != expected.ID || current.Session != expected.Session || current.Generation != expected.Generation {
		return service.ErrRecoveryConflict
	}
	if readOnly {
		if expected.Lease != "" && current.State == "busy" && current.Lease == expected.Lease && current.Operation == expected.Operation {
			return nil
		}
		if current.State != "ready" {
			return service.ErrRecoveryBusy
		}
		return nil
	}
	if current.State != "busy" || current.Lease != expected.Lease || current.Operation != expected.Operation {
		return service.ErrRecoveryConflict
	}
	return nil
}
func (r *accountRepository) BindRecoveryAccount(ctx context.Context, p service.RecoveryRow, account int64, principal string, readOnly bool) error {
	tx, e := r.recoveryTx(ctx)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback() }()
	cur, e := readRecovery(ctx, tx, p.Scope, true)
	if e != nil {
		return e
	}
	if e = requireRecoveryRow(cur, p, readOnly); e != nil {
		return e
	}
	if cur.AccountID != 0 && (cur.AccountID != account || cur.Principal != principal) {
		return service.ErrRecoveryConflict
	}
	_, e = tx.ExecContext(ctx, `UPDATE claude_recovery_segments SET account_id=$3,principal_hash=$4 WHERE session_id=$1 AND conversation_id=$2 AND (account_id IS NULL OR (account_id=$3 AND principal_hash=$4))`, p.Session, p.ID, account, principal)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (r *accountRepository) MarkRecoverySent(ctx context.Context, p service.RecoveryRow) error {
	return recoveryAffected(r.sql.ExecContext(ctx, `UPDATE claude_recovery_operations o SET state='sent' FROM claude_recovery_conversations c WHERE c.id=o.conversation_id AND `+recoveryScopeSQL+` AND c.id=$4 AND c.active_session_id=$5 AND c.generation=$6 AND c.lease_token=$7 AND c.lease_until>NOW() AND c.state='busy' AND o.id=$8 AND o.state='prepared'`, append(recoveryScopeArgs(p.Scope), p.ID, p.Session, p.Generation, p.Lease, p.Operation)...))
}
func (r *accountRepository) RenewRecoveryLease(ctx context.Context, p service.RecoveryRow, lease time.Duration) error {
	return recoveryAffected(r.sql.ExecContext(ctx, `UPDATE claude_recovery_conversations c SET lease_until=NOW()+$6*INTERVAL '1 second' WHERE `+recoveryScopeSQL+` AND c.id=$4 AND c.lease_token=$5 AND c.state='busy' AND c.lease_until>NOW()`, append(recoveryScopeArgs(p.Scope), p.ID, p.Lease, lease.Seconds())...))
}
func (r *accountRepository) CheckRecoveryVersion(ctx context.Context, p service.RecoveryRow) error {
	cur, e := readRecovery(ctx, r.sql, p.Scope, false)
	if e != nil {
		return e
	}
	return requireRecoveryRow(cur, p, true)
}
func (r *accountRepository) FinishRecovery(ctx context.Context, f service.RecoveryFinish) error {
	tx, e := r.recoveryTx(ctx)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback() }()
	cur, e := readRecovery(ctx, tx, f.Row.Scope, true)
	if e != nil {
		return e
	}
	if e = requireRecoveryRow(cur, f.Row, false); e != nil {
		return e
	}
	if f.State != "completed" && f.State != "failed" && f.State != "uncertain" {
		return service.ErrRecoveryConflict
	}
	if e = recoveryAffected(tx.ExecContext(ctx, `UPDATE claude_recovery_operations SET state=$3,result=$4 WHERE id=$1 AND conversation_id=$2 AND state IN ('prepared','sent')`, f.Row.Operation, f.Row.ID, f.State, f.Result)); e != nil {
		return e
	}
	state := "ready"
	if f.State == "uncertain" {
		state = "uncertain"
	}
	_, e = tx.ExecContext(ctx, `UPDATE claude_recovery_conversations SET state=$2,lease_token=NULL,lease_until=NULL,
 material=COALESCE($3,material),source=COALESCE($4,source),source_version=source_version+CASE WHEN $4::bytea IS NULL THEN 0 ELSE 1 END,
 summary_state=CASE WHEN $4::bytea IS NULL THEN summary_state ELSE 'pending' END,migration_blocked=migration_blocked OR $5,
 expires_at=NOW()+$6*INTERVAL '1 second' WHERE id=$1`, f.Row.ID, state, f.Material, f.Source, f.BlockMigration, f.Retention.Seconds())
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (r *accountRepository) MigrateRecovery(ctx context.Context, p service.RecoveryRow, next string, account int64, principal string, restore []byte, reason string) (*service.RecoveryRow, error) {
	tx, e := r.recoveryTx(ctx)
	if e != nil {
		return nil, e
	}
	defer func() { _ = tx.Rollback() }()
	cur, e := readRecovery(ctx, tx, p.Scope, true)
	if e != nil {
		return nil, e
	}
	if e = requireRecoveryRow(cur, p, false); e != nil {
		return nil, e
	}
	if cur.MigrationBlocked || cur.AccountID == account {
		return nil, service.ErrRecoveryConflict
	}
	var state string
	if e = tx.QueryRowContext(ctx, `SELECT state FROM claude_recovery_operations WHERE id=$1 AND conversation_id=$2`, p.Operation, p.ID).Scan(&state); e != nil {
		return nil, e
	}
	if state != "prepared" {
		return nil, service.ErrRecoveryUncertain
	}
	if e = recoveryAffected(tx.ExecContext(ctx, `INSERT INTO claude_session_ownership(session_id,account_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, next, account)); e != nil {
		return nil, e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO claude_recovery_segments(session_id,conversation_id,generation,account_id,principal_hash,restore,predecessor,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, next, p.ID, p.Generation+1, account, principal, restore, p.Session, reason)
	if e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE claude_recovery_operations SET session_id=$2 WHERE id=$1 AND state='prepared'`, p.Operation, next); e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE claude_recovery_conversations SET active_session_id=$2,generation=generation+1 WHERE id=$1`, p.ID, next); e != nil {
		return nil, e
	}
	cur.Session, cur.Generation, cur.AccountID, cur.Principal, cur.Restore = next, p.Generation+1, account, principal, restore
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return cur, nil
}

func (r *accountRepository) ClaimRecoverySummary(ctx context.Context, lease time.Duration, groups []int64, everyTurns int) (*service.RecoverySummaryJob, error) {
	tx, e := r.recoveryTx(ctx)
	if e != nil {
		return nil, e
	}
	defer func() { _ = tx.Rollback() }()
	var scope service.RecoveryScope
	e = tx.QueryRowContext(ctx, `SELECT user_id,group_id,client_session_id::text FROM claude_recovery_conversations WHERE group_id=ANY($1) AND source IS NOT NULL AND expires_at>NOW() AND summary_state IN ('pending','working') AND (summary_lease IS NULL OR summary_lease_until<NOW()) AND (checkpoint_version=0 OR source_version-checkpoint_version >= $2) ORDER BY expires_at LIMIT 1 FOR UPDATE SKIP LOCKED`, pq.Array(groups), everyTurns).Scan(&scope.UserID, &scope.GroupID, &scope.ClientSession)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	row, e := readRecovery(ctx, tx, scope, false)
	if e != nil {
		return nil, e
	}
	token := uuid.NewString()
	if _, e = tx.ExecContext(ctx, `UPDATE claude_recovery_conversations SET summary_state='working',summary_lease=$2,summary_lease_until=NOW()+$3*INTERVAL '1 second' WHERE id=$1`, row.ID, token, lease.Seconds()); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return &service.RecoverySummaryJob{Row: *row, Token: token}, nil
}
func (r *accountRepository) SaveRecoverySummary(ctx context.Context, j service.RecoverySummaryJob, payload []byte, success bool) error {
	state := "failed"
	if success {
		state = "idle"
	}
	return recoveryAffected(r.sql.ExecContext(ctx, `UPDATE claude_recovery_conversations c SET checkpoint=CASE WHEN $7 THEN $8 ELSE checkpoint END,checkpoint_version=CASE WHEN $7 THEN $6 ELSE checkpoint_version END,summary_state=CASE WHEN source_version>$6 THEN 'pending' ELSE $9 END,summary_lease=NULL,summary_lease_until=NULL WHERE `+recoveryScopeSQL+` AND c.id=$4 AND summary_lease=$5 AND checkpoint_version<=$6 AND expires_at>NOW()`, append(recoveryScopeArgs(j.Row.Scope), j.Row.ID, j.Token, j.Row.SourceVersion, success, payload, state)...))
}
func (r *accountRepository) ReserveRecoverySummaryBudget(ctx context.Context, user int64, max int) error {
	return recoveryAffected(r.sql.ExecContext(ctx, `INSERT INTO claude_recovery_summary_budget(user_id,day,calls) VALUES($1,(NOW() AT TIME ZONE 'UTC')::date,1) ON CONFLICT(user_id,day) DO UPDATE SET calls=claude_recovery_summary_budget.calls+1 WHERE claude_recovery_summary_budget.calls<$2`, user, max))
}
func (r *accountRepository) RecordRecoverySummaryUsage(ctx context.Context, user int64, u service.RecoverySummaryUsage) error {
	_, e := r.sql.ExecContext(ctx, `UPDATE claude_recovery_summary_budget SET input_tokens=input_tokens+$2,output_tokens=output_tokens+$3,unknown_outcomes=unknown_outcomes+CASE WHEN $4 THEN 0 ELSE 1 END WHERE user_id=$1 AND day=(NOW() AT TIME ZONE 'UTC')::date`, user, u.InputTokens, u.OutputTokens, u.Confirmed)
	return e
}
func (r *accountRepository) PurgeRecoveryMaterial(ctx context.Context) error {
	// Keep ownership/operation tombstones. Encrypted content alone is expirable.
	tx, e := r.recoveryTx(ctx)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback() }()
	_, e = tx.ExecContext(ctx, `WITH expired AS (SELECT id FROM claude_recovery_conversations WHERE expires_at<NOW() AND (state!='busy' OR lease_until<NOW()) AND state!='expired' LIMIT 100 FOR UPDATE SKIP LOCKED) UPDATE claude_recovery_conversations c SET material=NULL,source=NULL,checkpoint=NULL,state='expired',summary_state='idle' FROM expired e WHERE c.id=e.id`)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE claude_recovery_segments s SET restore=NULL FROM claude_recovery_conversations c WHERE s.conversation_id=c.id AND c.state='expired' AND s.restore IS NOT NULL`)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE claude_recovery_operations SET result=NULL WHERE expires_at<NOW() AND result IS NOT NULL`)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (r *accountRepository) IsRecoveryUpstreamSession(ctx context.Context, sid string) (bool, error) {
	rows, e := r.sql.QueryContext(ctx, `SELECT 1 FROM claude_recovery_segments WHERE session_id=$1`, sid)
	if e != nil {
		return false, e
	}
	defer func() { _ = rows.Close() }()
	found := rows.Next()
	return found, rows.Err()
}

func (r *accountRepository) HasRecoveryConversation(ctx context.Context, scope service.RecoveryScope) (bool, error) {
	if !validRecoveryScope(scope) {
		return false, service.ErrRecoveryConflict
	}
	rows, e := r.sql.QueryContext(ctx, `SELECT 1 FROM claude_recovery_conversations c WHERE `+recoveryScopeSQL, recoveryScopeArgs(scope)...)
	if e != nil {
		return false, e
	}
	defer func() { _ = rows.Close() }()
	found := rows.Next()
	return found, rows.Err()
}
