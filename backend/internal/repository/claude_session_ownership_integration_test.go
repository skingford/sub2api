//go:build integration

package repository

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestClaudeSessionOwnershipConcurrentClaimsAndPersistence(t *testing.T) {
	ctx := context.Background()
	session := uuid.NewString()
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM claude_session_ownership WHERE session_id=$1`, session)
	})
	const workers = 32
	winners := make([]int64, workers)
	errs := make([]error, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			repo := newAccountRepositoryWithSQL(nil, integrationDB, nil)
			winners[i], errs[i] = repo.ClaimClaudeSessionAccountID(ctx, session, int64(i+1))
		}(i)
	}
	close(start)
	wg.Wait()
	for i := range workers {
		require.NoError(t, errs[i])
		require.Positive(t, winners[i])
		require.Equal(t, winners[0], winners[i])
	}
	// A fresh repository instance and an old creation date must not expire the
	// binding. The owner is retained even without a live accounts row (tombstone).
	_, err := integrationDB.ExecContext(ctx, `UPDATE claude_session_ownership SET created_at=NOW()-INTERVAL '10 years' WHERE session_id=$1`, session)
	require.NoError(t, err)
	restarted := newAccountRepositoryWithSQL(nil, integrationDB, nil)
	winner, err := restarted.ClaimClaudeSessionAccountID(ctx, session, 999999)
	require.NoError(t, err)
	require.Equal(t, winners[0], winner)
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM claude_session_ownership WHERE session_id=$1`, session).Scan(&count))
	require.Equal(t, 1, count)
}
