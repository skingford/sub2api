//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/stretchr/testify/require"
)

func TestGetClaudeCodeClientVersionPriority(t *testing.T) {
	for _, tt := range []struct {
		name   string
		manual string
		synced string
		want   string
	}{
		{name: "手动值优先", manual: "2.1.280", synced: "2.1.281", want: "2.1.280"},
		{name: "归一化手动值", manual: " v2.1.280 ", synced: "2.1.281", want: "2.1.280"},
		{name: "跟随已验证同步值", synced: "2.1.295", want: "2.1.295"},
		{name: "未验证新版选择已有配置", synced: "2.1.300", want: "2.1.295"},
		{name: "两配置间版本选择较低已验证版本", synced: "2.1.294", want: "2.1.292"},
		{name: "旧发现值回退内置配置", synced: "2.1.281", want: claude.CLIVersion()},
		{name: "无效手动值回退", manual: "invalid", synced: "2.1.300", want: "2.1.295"},
		{name: "无效同步值回退", synced: "2.1.9", want: claude.CLIVersion()},
		{name: "未配置时回退", want: claude.CLIVersion()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &authSourceDefaultsRepoStub{values: map[string]string{
				SettingKeyClaudeCodeClientVersion:       tt.manual,
				SettingKeyClaudeCodeClientVersionSynced: tt.synced,
			}}
			svc := NewSettingService(repo, &config.Config{})
			require.Equal(t, tt.want, svc.GetClaudeCodeClientVersion(context.Background()))
		})
	}
}

func TestUpdateSettingsClaudeCodeVersionTakesEffectImmediately(t *testing.T) {
	ctx := context.Background()
	repo := &authSourceDefaultsRepoStub{values: map[string]string{
		SettingKeyClaudeCodeClientVersionSynced: "2.1.300",
	}}
	svc := NewSettingService(repo, &config.Config{})
	resetGatewayForwardingSettingsCacheForTest(t)
	defer svc.refreshCachedSettings(&SystemSettings{})
	require.Equal(t, "2.1.295", svc.GetClaudeCodeClientVersion(ctx))

	settings := &SystemSettings{
		ClaudeCodeClientVersion:          "2.1.292",
		ClaudeCodeVersionAutoSyncEnabled: true,
	}
	require.NoError(t, svc.UpdateSettings(ctx, settings))
	require.Equal(t, "2.1.292", svc.GetClaudeCodeClientVersion(ctx), "保存手动版本后应立即生效")
	require.NotContains(t, repo.updates, SettingKeyClaudeCodeClientVersionSynced)
	require.Equal(t, "2.1.300", repo.values[SettingKeyClaudeCodeClientVersionSynced])

	// 清空手动值并关闭后续同步时，仍保留并使用已有同步值。
	settings.ClaudeCodeClientVersion = ""
	settings.ClaudeCodeVersionAutoSyncEnabled = false
	require.NoError(t, svc.UpdateSettings(ctx, settings))
	require.Equal(t, "2.1.295", svc.GetClaudeCodeClientVersion(ctx))
	require.Equal(t, "false", repo.values[SettingKeyClaudeCodeVersionAutoSyncEnabled])
}

func TestClaudeCodeSettingsShowDiscoveryAndSelectedProfile(t *testing.T) {
	svc := NewSettingService(&authSourceDefaultsRepoStub{}, &config.Config{})
	for _, manual := range []string{"", "2.1.292", "2.1.999"} {
		values := map[string]string{SettingKeyClaudeCodeClientVersion: manual, SettingKeyClaudeCodeClientVersionSynced: "2.1.300"}
		settings := svc.parseSettings(values)
		require.Equal(t, "2.1.300", settings.ClaudeCodeClientVersionSynced)
		want := "2.1.295"
		if manual != "" {
			want = manual
		}
		require.Equal(t, want, settings.ClaudeCodeClientVersionEffective)
		require.Equal(t, claude.IsVerifiedCLIVersion(want), settings.ClaudeCodeClientVersionVerified)
	}
}
