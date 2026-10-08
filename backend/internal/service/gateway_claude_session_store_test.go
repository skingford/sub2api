package service

import (
	"context"
	"sync"
)

type memoryClaudeSessionStore struct {
	mu     sync.Mutex
	owners map[string]int64
	err    error
}

func (m *memoryClaudeSessionStore) GetClaudeSessionAccountID(_ context.Context, session string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.owners[session], m.err
}
func (m *memoryClaudeSessionStore) ClaimClaudeSessionAccountID(_ context.Context, session string, account int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return 0, m.err
	}
	if m.owners == nil {
		m.owners = map[string]int64{}
	}
	if m.owners[session] == 0 {
		m.owners[session] = account
	}
	return m.owners[session], nil
}
