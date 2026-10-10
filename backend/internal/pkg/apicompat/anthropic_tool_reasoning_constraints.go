package apicompat

import (
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
)

// With no tools the parallel setting is vacuous. With tools it belongs on
// Anthropic's choice object; type=none already forbids every tool call and has
// no disable_parallel_tool_use field in its wire shape.
func anthropicParallelToolChoice(raw json.RawMessage, parallel bool) (json.RawMessage, error) {
	choice := map[string]json.RawMessage{"type": rawJSONString("auto")}
	if len(normalizedRawJSON(raw)) > 0 {
		var ok bool
		choice, ok = rawJSONObject(raw)
		if !ok || choice == nil {
			return nil, fmt.Errorf("parallel_tool_calls requires a valid tool_choice object")
		}
	}
	switch rawString(choice["type"]) {
	case "none":
		return raw, nil
	case "auto", "any", "tool":
	default:
		return nil, fmt.Errorf("parallel_tool_calls cannot be combined with unknown tool_choice")
	}
	choice["disable_parallel_tool_use"], _ = json.Marshal(!parallel)
	return json.Marshal(choice)
}

func validateAnthropicStrictTools(tools []ResponsesTool) error {
	for _, tool := range tools {
		if tool.Strict == nil || !*tool.Strict {
			continue
		}
		switch tool.Type {
		case "web_search", "google_search", "web_search_20250305":
			return fmt.Errorf("strict is not supported for server tool %q", tool.Type)
		}
		obj, ok := rawJSONObject(tool.Parameters)
		if !ok || rawString(obj["type"]) != "object" {
			return fmt.Errorf("strict tool %q requires an object input schema", tool.Name)
		}
	}
	return nil
}

func anthropicToolInputSchema(tool ResponsesTool) json.RawMessage {
	if tool.Strict != nil && *tool.Strict {
		// Flattening a union or filling a missing schema can weaken explicit
		// strict constraints. Keep the validated schema's original contents.
		return append(json.RawMessage(nil), tool.Parameters...)
	}
	return normalizeAnthropicInputSchema(tool.Parameters)
}

func applyAnthropicLegacyReasoning(req *ResponsesRequest, out *AnthropicRequest) error {
	if req.Reasoning == nil || req.Reasoning.Effort == "" {
		return nil
	}
	effort := req.Reasoning.Effort
	if effort == "none" {
		out.Thinking = &AnthropicThinking{Type: "disabled"}
		return nil
	}
	switch effort {
	case "low", "medium", "high", "xhigh", "max":
	default:
		return fmt.Errorf("unsupported Anthropic reasoning effort %q", effort)
	}
	effort = mapResponsesEffortToAnthropic(effort)
	setAnthropicOutputEffort(out, effort)
	// Keep the existing low-effort policy. A forced tool selection can be
	// honored without enabling thinking; stronger explicit reasoning cannot.
	if effort == "low" {
		return nil
	}
	var choice struct {
		Type string `json:"type"`
	}
	if len(out.ToolChoice) > 0 {
		if err := json.Unmarshal(out.ToolChoice, &choice); err != nil {
			return fmt.Errorf("invalid tool_choice: %w", err)
		}
	}
	if choice.Type == "any" || choice.Type == "tool" {
		return fmt.Errorf("forced tool_choice cannot be combined with enabled reasoning")
	}
	switch claude.NormalizeModelID(req.Model) {
	case "claude-sonnet-4-6", "claude-opus-4-6":
		out.Thinking = &AnthropicThinking{Type: "adaptive"}
		return nil
	}
	// Manual thinking needs at least 1,024 tokens and must fit below the
	// caller's cap. Reject an impossible combination rather than increasing
	// that cap or silently disabling the requested reasoning.
	if out.MaxTokens <= 1024 {
		return fmt.Errorf("enabled reasoning requires max_tokens greater than 1024 for %s", req.Model)
	}
	out.Thinking = &AnthropicThinking{Type: "enabled", BudgetTokens: min(defaultThinkingBudget(effort), out.MaxTokens-1)}
	return nil
}
