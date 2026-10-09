package service

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/tidwall/gjson"
)

func claude2292VerifiedModel(model string) bool {
	switch model {
	case "claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5-20251001", "claude-sonnet-5-5", "claude-opus-5-5":
		return true
	}
	return false
}

// Defaults are limited to models captured with the pinned official executable.
// Explicit values win. Temperature is a late scalar override in the native CLI;
// by itself it does not disable the model's default thinking configuration.
func claude2292ModelDefaults(body []byte, model string) []byte {
	maxTokens := 32000
	if model == "claude-opus-4-6" {
		maxTokens = 64000
	}
	if isClaude55SignedThinkingModel(model) {
		maxTokens = 128000
	}
	if !gjson.GetBytes(body, "max_tokens").Exists() {
		body, _ = setJSONValueBytes(body, "max_tokens", maxTokens)
	}
	choice := gjson.GetBytes(body, "tool_choice.type").String()
	if !gjson.GetBytes(body, "thinking").Exists() &&
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
	thinking := gjson.GetBytes(body, "thinking")
	thinkingType := thinking.Get("type").String()
	if thinkingType == "disabled" {
		// Native _ur removes every extra property from disabled thinking. This
		// is a direct thinking control, equivalent to MAX_THINKING_TOKENS=0;
		// it must not silently retain an incompatible display or budget.
		hasExtra := false
		thinking.ForEach(func(key, _ gjson.Result) bool {
			hasExtra = key.String() != "type"
			return !hasExtra
		})
		if hasExtra {
			body, _ = setJSONRawBytes(body, "thinking", []byte(`{"type":"disabled"}`))
		}
		if !isClaude55SignedThinkingModel(model) && !gjson.GetBytes(body, "temperature").Exists() {
			body, _ = setJSONValueBytes(body, "temperature", 1)
		}
	}
	if thinkingType != "" && model != "claude-haiku-4-5-20251001" && !gjson.GetBytes(body, "output_config.effort").Exists() {
		effort := "high"
		if isClaude55SignedThinkingModel(model) {
			effort = "medium"
		}
		body, _ = setJSONValueBytes(body, "output_config.effort", effort)
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
	if isClaude55SignedThinkingModel(model) {
		betas = []string{claude.BetaClaudeCode, claude.BetaOAuth, claude.BetaInterleavedThinking,
			claude.BetaThinkingTokenCount, claude.BetaContextManagement, claude.BetaPromptCachingScope,
			claude.BetaMidConversationSystem, claude.BetaPerTurnControl, claude.BetaMidConversationToolChanges, claude.BetaMidConversationSystemClear,
			claude.BetaEffort, claude.BetaThinkingBindingControls, claude.BetaExtendedCacheTTL}
	}
	if claude.NormalizeModelID(model) == "claude-haiku-4-5-20251001" {
		betas = []string{claude.BetaOAuth, claude.BetaInterleavedThinking, claude.BetaThinkingTokenCount,
			claude.BetaContextManagement, claude.BetaPromptCachingScope, claude.BetaClaudeCode,
			claude.BetaThinkingBindingControls, claude.BetaExtendedCacheTTL}
	}
	if gjson.GetBytes(body, "thinking.display").String() == "updates" {
		// Native CLI adds this capability only when updates are requested. Keep
		// its position before OAuth's extended cache TTL and diagnostics.
		betas = append(betas[:len(betas)-1], claude.BetaThinkingDisplayUpdates, claude.BetaExtendedCacheTTL)
	}
	gjson.GetBytes(body, "messages").ForEach(func(_, message gjson.Result) bool {
		if message.Get("output_config").Exists() && !isClaude55SignedThinkingModel(model) {
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
