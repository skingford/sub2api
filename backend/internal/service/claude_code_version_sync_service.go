package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
)

const (
	// claudeCodeVersionSyncInterval 自动同步间隔。Claude Code CLI 发版频率是小时级，
	// 1 小时足够跟上官方节奏，同时把对 GitHub API 的调用压到每天 24 次。
	claudeCodeVersionSyncInterval = time.Hour
	// claudeCodeVersionSyncTimeout 单次同步的整体超时。
	claudeCodeVersionSyncTimeout = 30 * time.Second
	// claudeCodeVersionSyncRepo 官方 Claude Code CLI 仓库。
	claudeCodeVersionSyncRepo = "anthropics/claude-code"
	// claudeCodeVersionSyncPerPage 回退路径单次拉取的 release 数量（主路径见
	// fetchLatestStableVersion）。
	claudeCodeVersionSyncPerPage = 30
	// claudeCodeVersionTagPrefix 客户端 release 的 tag 前缀（如 v2.1.280）。
	// 同仓库的 tag 家族单一，但显式按前缀过滤仍能挡住上游改版或混入的其他 tag。
	claudeCodeVersionTagPrefix = "v"
)

// ClaudeCodeVersionSyncService records the newest discovered stable release.
// Discovery does not activate an unverified profile: runtime selection uses the
// same curated registry as the gateway's conversion guard.
//
// 同步值写入 SettingKeyClaudeCodeClientVersionSynced（本服务独占写入）；管理员在面板填写的
// SettingKeyClaudeCodeClientVersion 优先级更高，因此手工固定版本不会被同步覆盖。
type ClaudeCodeVersionSyncService struct {
	settingRepo    SettingRepository
	settingService *SettingService
	githubClient   GitHubReleaseClient
	interval       time.Duration
	lifecycleMu    sync.Mutex
	started        bool
	stopped        bool
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
}

func NewClaudeCodeVersionSyncService(
	settingRepo SettingRepository,
	settingService *SettingService,
	githubClient GitHubReleaseClient,
	interval time.Duration,
) *ClaudeCodeVersionSyncService {
	ctx, cancel := context.WithCancel(context.Background())
	return &ClaudeCodeVersionSyncService{
		settingRepo:    settingRepo,
		settingService: settingService,
		githubClient:   githubClient,
		interval:       interval,
		ctx:            ctx, cancel: cancel,
	}
}

func (s *ClaudeCodeVersionSyncService) Start() {
	if s == nil || s.settingRepo == nil || s.githubClient == nil || s.interval <= 0 {
		return
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.started || s.stopped {
		return
	}
	s.started = true
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		s.runInitial()
		for {
			select {
			case <-ticker.C:
				s.runOnce()
			case <-s.ctx.Done():
				return
			}
		}
	}()
}

func (s *ClaudeCodeVersionSyncService) Stop() {
	if s == nil {
		return
	}
	s.lifecycleMu.Lock()
	s.stopped = true
	s.cancel()
	s.lifecycleMu.Unlock()
	s.wg.Wait()
}

// runInitial 执行启动时的首次同步。若一个同步周期内已有成功检查则跳过：
// 频繁重启、滚动发布或崩溃重启会让「启动即同步」放大成对 GitHub 的连续请求，
// 而同步间隔只有 1 小时，重启后没有立刻重新拉取的必要。
func (s *ClaudeCodeVersionSyncService) runInitial() {
	if s.syncedWithinInterval() {
		return
	}
	s.runOnce()
}

// Prefer the last successful check. Older installations fall back to the
// discovery row timestamp until the first successful check writes the new key.
func (s *ClaudeCodeVersionSyncService) syncedWithinInterval() bool {
	if s.interval <= 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(s.ctx, claudeCodeVersionSyncTimeout)
	defer cancel()
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyClaudeCodeVersionLastCheckedAt)
	if err == nil && raw != "" {
		checked, parseErr := time.Parse(time.RFC3339Nano, raw)
		age := time.Since(checked)
		return parseErr == nil && age >= 0 && age < s.interval
	}
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return false
	}
	setting, err := s.settingRepo.Get(ctx, SettingKeyClaudeCodeClientVersionSynced)
	if err != nil || setting == nil || setting.UpdatedAt.IsZero() || NormalizeClaudeCodeClientVersion(setting.Value) == "" {
		return false
	}
	age := time.Since(setting.UpdatedAt)
	return age >= 0 && age < s.interval
}

func (s *ClaudeCodeVersionSyncService) runOnce() {
	ctx, cancel := context.WithTimeout(s.ctx, claudeCodeVersionSyncTimeout)
	defer cancel()

	if ctx.Err() != nil || !s.autoSyncEnabled(ctx) {
		return
	}

	latest := s.fetchLatestStableVersion(ctx)
	if latest == "" {
		return
	}

	// Conditional writes prevent an older concurrent instance from replacing a
	// newer discovery. Missing atomic support is an error, never an unsafe fallback.
	changed, err := s.advanceSetting(ctx, SettingKeyClaudeCodeClientVersionSynced, latest, func(current string) bool {
		version := NormalizeClaudeCodeClientVersion(current)
		return version == "" || CompareVersions(latest, version) > 0
	})
	if err != nil {
		slog.Warn("claude_code_version_sync_persist_failed", "version", latest, "error", err)
		return
	}
	if s.settingService != nil {
		s.settingService.InvalidateClaudeCodeClientVersionCache()
	}
	checked := time.Now().UTC()
	_, err = s.advanceSetting(ctx, SettingKeyClaudeCodeVersionLastCheckedAt, checked.Format(time.RFC3339Nano), func(current string) bool {
		previous, e := time.Parse(time.RFC3339Nano, current)
		return e != nil || previous.After(checked.Add(s.interval)) || previous.Before(checked)
	})
	if err != nil {
		slog.Warn("claude_code_version_sync_check_time_failed", "error", err)
	}
	if changed {
		slog.Info("claude_code_version_synced", "version", latest,
			"verified_automatic_candidate", claude.VerifiedCLIVersionAtOrBelow(latest))
	}
}

func (s *ClaudeCodeVersionSyncService) advanceSetting(ctx context.Context, key, next string, shouldAdvance func(string) bool) (bool, error) {
	atomicRepo, ok := s.settingRepo.(SettingCompareAndSwapper)
	if !ok {
		return false, fmt.Errorf("settings repository does not support atomic discovery updates")
	}
	for range 8 {
		current, err := s.settingRepo.GetValue(ctx, key)
		if err != nil && !errors.Is(err, ErrSettingNotFound) {
			return false, err
		}
		if !shouldAdvance(current) {
			return false, nil
		}
		changed, err := atomicRepo.CompareAndSwap(ctx, key, current, next)
		if err != nil || changed {
			return changed, err
		}
		if err = ctx.Err(); err != nil {
			return false, err
		}
	}
	return false, fmt.Errorf("settings discovery update contention")
}

// fetchLatestStableVersion 取官方最新稳定版 CLI 版本号；取不到时返回空串，
// 由调用方保持既有值（不清空、不降级），各失败分支自行落日志。
//
// 主路径 /releases/latest：该端点本身就排除 draft 与 prerelease，直接给出最新正式发布。
//
// 回退列表扫描：latest 抓取失败或返回值未通过过滤时，扫一页 release 继续跟随官方版本，
// 否则版本号会静默停更。两条路径共用同一套过滤（前缀 / draft / prerelease / 版本号形态），
// 语义不会分叉。
func (s *ClaudeCodeVersionSyncService) fetchLatestStableVersion(ctx context.Context) string {
	release, err := s.githubClient.FetchLatestRelease(ctx, claudeCodeVersionSyncRepo)
	if err != nil {
		slog.Warn("claude_code_version_sync_latest_fetch_failed", "error", err)
	} else if version := latestClaudeCodeStableReleaseVersion([]*GitHubRelease{release}); version != "" {
		return version
	}

	if ctx.Err() != nil {
		return ""
	}

	// 主路径没拿到可用版本（抓取失败，或 latest 未通过过滤）。
	releases, err := s.githubClient.FetchRecentReleases(ctx, claudeCodeVersionSyncRepo, claudeCodeVersionSyncPerPage)
	if err != nil {
		slog.Warn("claude_code_version_sync_fetch_failed", "error", err)
		return ""
	}
	version := latestClaudeCodeStableReleaseVersion(releases)
	if version == "" {
		slog.Warn("claude_code_version_sync_no_stable_release", "repo", claudeCodeVersionSyncRepo)
	}
	return version
}

// autoSyncEnabled 读取面板开关。缺失或空值视为开启，与设置默认值一致；
// 读取失败时保持开启，避免一次数据库抖动就静默停掉版本跟随。
func (s *ClaudeCodeVersionSyncService) autoSyncEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyClaudeCodeVersionAutoSyncEnabled)
	if err != nil {
		return true
	}
	if strings.TrimSpace(value) == "" {
		return true
	}
	return strings.TrimSpace(value) == "true"
}

// latestClaudeCodeStableReleaseVersion 从 release 列表里挑出最大的稳定版 CLI 版本号。
// 过滤条件：tag 前缀为 v、非草稿、非预发布、版本号须通过 NormalizeClaudeCodeClientVersion
// （严格三段纯数字 semver，且不低于内置基线）。取最大值而非最新发布，
// 避免重新发布历史 tag 造成回退。
// 主路径的单条 /releases/latest 结果也走本函数（单元素切片），保证两条取数路径的过滤语义一致。
func latestClaudeCodeStableReleaseVersion(releases []*GitHubRelease) string {
	best := ""
	for _, release := range releases {
		if release == nil || release.Draft || release.Prerelease {
			continue
		}
		tag := strings.TrimSpace(release.TagName)
		if !strings.HasPrefix(tag, claudeCodeVersionTagPrefix) {
			continue
		}
		version := NormalizeClaudeCodeClientVersion(strings.TrimPrefix(tag, claudeCodeVersionTagPrefix))
		if version == "" || strings.Contains(version, "-") {
			continue
		}
		if best == "" || CompareVersions(version, best) > 0 {
			best = version
		}
	}
	return best
}
