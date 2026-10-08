package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.ClaudeSessionStore = (*accountRepository)(nil)

func (r *accountRepository) GetClaudeSessionAccountID(ctx context.Context, sessionID string) (int64, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT account_id FROM claude_session_ownership WHERE session_id = $1::uuid`, sessionID)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return 0, rows.Err()
	}
	var accountID int64
	if err := rows.Scan(&accountID); err != nil {
		return 0, err
	}
	return accountID, rows.Err()
}

func (r *accountRepository) ClaimClaudeSessionAccountID(ctx context.Context, sessionID string, accountID int64) (int64, error) {
	// The unique constraint serializes competing first requests across replicas.
	// A separate SELECT sees a concurrent winner after INSERT has waited for its
	// commit. Never update the owner, including on account deletion or cooldown.
	_, err := r.sql.ExecContext(ctx, `INSERT INTO claude_session_ownership (session_id, account_id)
        VALUES ($1::uuid, $2) ON CONFLICT (session_id) DO NOTHING`, sessionID, accountID)
	if err != nil {
		return 0, err
	}
	return r.GetClaudeSessionAccountID(ctx, sessionID)
}
