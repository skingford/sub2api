package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func (s *GatewayService) preserveClaudeOAuthCaller() bool {
	return s != nil && s.cfg != nil && s.cfg.Gateway.ClaudeOAuthPreserveCaller
}

// rewriteClaudeOAuthSystem is used only after classifying a request as an
// API-to-OAuth conversion. Native requests must never enter this policy.
func (s *GatewayService) rewriteClaudeOAuthSystem(ctx context.Context, c *gin.Context, body []byte, system any, model string) ([]byte, error) {
	enabled, prompt, blocks := s.claudeOAuthSystemPromptInjectionSettings(ctx)
	if !enabled {
		return body, nil
	}
	if !s.preserveClaudeOAuthCaller() {
		blocks = claudeOAuthSystemPromptBlocksForModel(model, blocks)
		return rewriteSystemForNonClaudeCodeWithPromptBlocks(body, system, prompt, blocks), nil
	}

	// API conversion uses the measured interactive CLI --system-prompt branch,
	// including explicit empty system when the caller supplied none. It does not
	// impersonate the default/append prompt, which depends on CLI tools and state.
	prefix, err := buildClaudeOAuthSystemPromptBlocksJSON(body, "", `[
		{"type":"text","text":"{billing_header}"},
		{"type":"text","text":"{claude_code_system_prompt}"}
	]`)
	if err != nil {
		return nil, fmt.Errorf("build Claude OAuth system prefix: %w", err)
	}
	caller := gjson.GetBytes(body, "system")
	canonicalText := false
	switch {
	case !caller.Exists() || caller.Type == gjson.Null:
		canonicalText = true
	case caller.Type == gjson.String:
		canonicalText = true
		// Do not trim, sanitize or inspect the text for identity keywords.
		if caller.String() != "" {
			raw, marshalErr := json.Marshal(anthropicSystemTextBlockPayload{Type: "text", Text: caller.String()})
			if marshalErr != nil {
				return nil, marshalErr
			}
			prefix = append(prefix, raw)
		}
	case caller.IsArray():
		// Adapters represent one scalar instruction as one text block. Explicit
		// block layouts/cache choices remain an API extension, preserved as given.
		canonicalText = len(caller.Array()) <= 1
		for _, block := range caller.Array() {
			if !block.IsObject() || block.Get("type").String() != "text" || block.Get("text").Type != gjson.String {
				return nil, claudeCompatibilityError(c, "Claude OAuth system must contain text blocks")
			}
			if block.Get("cache_control").Exists() {
				canonicalText = false
			}
			// Raw blocks retain order, whitespace, cache policy and extension fields.
			prefix = append(prefix, []byte(block.Raw))
		}
	default:
		return nil, claudeCompatibilityError(c, "Claude OAuth system must be a string or text block array")
	}
	if canonicalText {
		// Both pinned interactive versions put 1h breakpoints on identity and
		// nonempty custom text. Never evict caller breakpoints to add defaults.
		_, messages, tools, system := collectCacheControlPaths(body)
		needed := len(prefix) - 1
		if len(messages)+len(tools)+len(system)+needed <= 4 {
			for i := 1; i < len(prefix); i++ {
				prefix[i], err = sjson.SetRawBytes(prefix[i], "cache_control", []byte(`{"type":"ephemeral","ttl":"1h"}`))
				if err != nil {
					return nil, err
				}
			}
		}
	}
	return sjson.SetRawBytes(body, "system", buildJSONArrayRaw(prefix))
}

// applyClaudeOAuthToolPolicy keeps all legacy tool/cache edits together so
// messages, adapters and token counting cannot select different policies.
func (s *GatewayService) applyClaudeOAuthToolPolicy(ctx context.Context, c *gin.Context, body []byte) []byte {
	if s.preserveClaudeOAuthCaller() {
		return body
	}
	body = s.rewriteMessageCacheControlIfEnabled(ctx, body)
	if rw := buildToolNameRewriteFromBody(body); rw != nil {
		body = applyToolNameRewriteToBody(body, rw)
		if c != nil {
			c.Set(toolNameRewriteKey, rw)
		}
		return body
	}
	return applyToolsLastCacheBreakpoint(body)
}

// Preserve valid caller breakpoints, or reject before dispatch. Silently
// deleting excess/invalid breakpoints would violate the selected policy.
func enforceClaudeCallerCachePolicy(c *gin.Context, body []byte, preserve bool) ([]byte, error) {
	if !preserve {
		return enforceCacheControlLimit(body), nil
	}
	invalid, messages, tools, system := collectCacheControlPaths(body)
	if len(invalid) != 0 {
		return nil, claudeCompatibilityError(c, "cache_control on thinking blocks is not supported")
	}
	if len(messages)+len(tools)+len(system) > 4 {
		return nil, claudeCompatibilityError(c, "Claude requests support at most four cache_control breakpoints")
	}
	return body, nil
}
