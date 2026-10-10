//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClaudeDiscoveryCannotActivateUnverifiedRelease(t *testing.T) {
	ctx := context.Background()
	repo := newClaudeCodeVersionSyncSettingRepoStub(map[string]string{SettingKeyClaudeCodeClientVersionSynced: "2.1.292"})
	settings := NewSettingService(repo, &config.Config{})
	require.Equal(t, "2.1.292", settings.GetClaudeCodeClientVersion(ctx)) // warm cache
	github := &claudeCodeVersionSyncGitHubStub{latest: &GitHubRelease{TagName: "v2.1.300"}}
	syncer := NewClaudeCodeVersionSyncService(repo, settings, github, time.Hour)
	syncer.runOnce()
	require.Equal(t, "2.1.300", repo.values[SettingKeyClaudeCodeClientVersionSynced], "keep discovery visible")
	require.Equal(t, "2.1.295", settings.GetClaudeCodeClientVersion(ctx), "cache must select the measured bundle")
	withCLIVersionResolverForTest(t, func() string { return settings.GetClaudeCodeClientVersion(ctx) })
	for _, route := range []string{"/v1/messages", "/v1/chat/completions", "/v1/responses", "/v1/messages/count_tokens"} {
		t.Run(route, func(t *testing.T) {
			svc, up := newClaudeContractGateway(t)
			svc.cfg.Gateway.ClaudeOAuthPreserveCaller = true
			payload := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}]}`)
			if route == "/v1/responses" {
				payload = []byte(`{"model":"claude-sonnet-4-6","input":"hi"}`)
			}
			_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), payload, nil, route)
			require.NoError(t, err)
			require.Equal(t, 200, rec.Code)
			require.Equal(t, 1, up.calls)
			require.Equal(t, "claude-cli/2.1.295 (external, cli)", getHeaderRaw(up.request.Header, "User-Agent"))
			require.Equal(t, HTTPUpstreamProfileClaude2295, HTTPUpstreamProfileFromContext(up.request.Context()))
		})
	}
	// Existing persisted future discoveries recover without another fetch.
	restarted := NewSettingService(repo, &config.Config{})
	require.Equal(t, "2.1.295", restarted.GetClaudeCodeClientVersion(ctx))
	require.Equal(t, 1, github.latestCalls)
}

func TestClaudeManualUnverifiedVersionIsNotSilentlyReplaced(t *testing.T) {
	repo := newClaudeCodeVersionSyncSettingRepoStub(map[string]string{SettingKeyClaudeCodeClientVersion: "2.1.300", SettingKeyClaudeCodeClientVersionSynced: "2.1.999"})
	settings := NewSettingService(repo, &config.Config{})
	withCLIVersionResolverForTest(t, func() string { return settings.GetClaudeCodeClientVersion(context.Background()) })
	svc, up := newClaudeContractGateway(t)
	_, rec, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}]}`), nil, "/v1/messages")
	require.ErrorContains(t, err, "unsupported Claude compatibility version")
	require.Equal(t, 400, rec.Code)
	require.Zero(t, up.calls)
	require.Equal(t, "2.1.300", repo.values[SettingKeyClaudeCodeClientVersion])
}

func TestVerifiedCLIRegistryHasCompleteGatewayProfiles(t *testing.T) {
	for _, version := range claude.VerifiedCLIVersions() {
		t.Run(version, func(t *testing.T) {
			withCLIVersionResolverForTest(t, func() string { return version })
			svc, up := newClaudeContractGateway(t)
			svc.cfg.Gateway.ClaudeOAuthPreserveCaller = true
			_, _, err := callClaudeContract(t, svc, newClaude2292Account(AccountTypeOAuth), []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}]}`), nil, "/v1/messages")
			require.NoError(t, err)
			require.Equal(t, "claude-cli/"+version+" (external, cli)", getHeaderRaw(up.request.Header, "User-Agent"))
			require.Contains(t, gjson.GetBytes(up.body, "system.0.text").String(), "cc_version="+version+".")
			require.Contains(t, gjson.GetBytes(up.body, "system.0.text").String(), " cch=")
			profile := HTTPUpstreamProfileFromContext(up.request.Context())
			require.NotEqual(t, HTTPUpstreamProfileDefault, profile, "a registry entry needs a measured transport")
			require.Equal(t, nativeClaudeTransportProfile(up.request.Header), profile)
		})
	}
}
