package service

import (
	"context"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// An explicit schema opts a converted first-party request into its measured
// capability. Native requests retain their own feature declarations. A policy
// or header override cannot silently disable a caller's required output format.
func (s *GatewayService) claudeOutputFormatBeta(ctx context.Context, c *gin.Context, account *Account, body []byte, target, beta string, drop map[string]struct{}) (string, error) {
	if account == nil || account.Platform != PlatformAnthropic ||
		gjson.GetBytes(body, "output_config.format.type").String() != "json_schema" ||
		preserveNativeClaudeRequest(ctx, c, account, body) {
		return beta, nil
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || u.User != nil || !strings.EqualFold(u.Hostname(), "api.anthropic.com") || (u.Port() != "" && u.Port() != "443") {
		return beta, nil
	}
	const token = claude.BetaStructuredOutputsNative
	policy := s.evaluateBetaPolicy(ctx, token, account, gjson.GetBytes(body, "model").String())
	_, filtered := policy.filterSet[token]
	_, dropped := drop[token]
	if policy.blockErr != nil || filtered || dropped {
		return beta, claudeCompatibilityError(c, "JSON Schema output is disabled by the account beta policy")
	}
	if containsBetaToken(beta, token) {
		return beta, nil
	}
	if _, overridden := account.HeaderOverrideValue("anthropic-beta"); overridden {
		return beta, claudeCompatibilityError(c, "anthropic-beta override omits the required structured output capability")
	}
	return mergeAnthropicBetaDropping(nil, strings.Trim(strings.TrimSpace(beta)+","+token, ","), nil), nil
}
