package service

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/tidwall/gjson"
)

// Defaults are limited to models captured with the pinned official executable.
// Explicit sampling, tool choice and thinking controls retain their semantics.
func claude2292ModelDefaults(body []byte, model string) []byte {
	maxTokens := 32000
	if model == "claude-opus-4-6" {
		maxTokens = 64000
	}
	if !gjson.GetBytes(body, "max_tokens").Exists() {
		body, _ = setJSONValueBytes(body, "max_tokens", maxTokens)
	}
	choice := gjson.GetBytes(body, "tool_choice.type").String()
	if !gjson.GetBytes(body, "thinking").Exists() && !gjson.GetBytes(body, "temperature").Exists() &&
		!gjson.GetBytes(body, "top_p").Exists() && !gjson.GetBytes(body, "top_k").Exists() &&
		choice != "any" && choice != "tool" {
		if model == "claude-haiku-4-5-20251001" {
			budget := min(gjson.GetBytes(body, "max_tokens").Int()-1, 31999)
			if budget >= 1024 {
				body, _ = setJSONValueBytes(body, "thinking", map[string]any{"type": "enabled", "display": "omitted", "budget_tokens": budget})
			}
		} else {
			body, _ = setJSONRawBytes(body, "thinking", []byte(`{"type":"adaptive","display":"omitted"}`))
		}
	}
	if gjson.GetBytes(body, "thinking.type").String() == "adaptive" && !gjson.GetBytes(body, "output_config.effort").Exists() {
		body, _ = setJSONValueBytes(body, "output_config.effort", "high")
	}
	return body
}

func claude2292CompatibilityBetas(model string, body []byte, countTokens bool) []string {
	if countTokens {
		return []string{claude.BetaClaudeCode, claude.BetaOAuth, claude.BetaInterleavedThinking, claude.BetaContextManagement, claude.BetaTokenCounting}
	}
	betas := []string{claude.BetaClaudeCode, claude.BetaOAuth, claude.BetaInterleavedThinking,
		claude.BetaThinkingTokenCount, claude.BetaContextManagement, claude.BetaPromptCachingScope,
		claude.BetaEffort, claude.BetaThinkingBindingControls, claude.BetaExtendedCacheTTL}
	if claude.NormalizeModelID(model) == "claude-haiku-4-5-20251001" {
		betas = []string{claude.BetaOAuth, claude.BetaInterleavedThinking, claude.BetaThinkingTokenCount,
			claude.BetaContextManagement, claude.BetaPromptCachingScope, claude.BetaClaudeCode,
			claude.BetaThinkingBindingControls, claude.BetaExtendedCacheTTL}
	}
	gjson.GetBytes(body, "messages").ForEach(func(_, message gjson.Result) bool {
		if message.Get("output_config").Exists() {
			betas = append(betas, claude.BetaMidConversationOutputConfig)
			return false
		}
		return true
	})
	// The caller must provide diagnostics state. The gateway cannot infer the
	// provider's previous message ID from an arbitrary conversation transcript.
	if gjson.GetBytes(body, "diagnostics").Exists() {
		betas = append(betas, claude.BetaCacheDiagnosis)
	}
	return betas
}
