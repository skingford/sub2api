package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/tidwall/gjson"
)

// Stored inside the scope-authenticated history ciphertext. The summary is the
// actual completed upstream reply, never a client-supplied checksum or marker.
type RecoveryCompaction struct {
	Summary          string            `json:"summary"`
	Prefix           int               `json:"prefix"`
	InputPrefix      int               `json:"input_prefix"`
	ContinuationTail []json.RawMessage `json:"continuation_tail,omitempty"`
}

type recoveryCompactionKey struct{}
type recoveryCompactionRequest struct {
	kind  string
	infer bool
}

// WithClaudeRecoveryRequest carries only the validated request kind. Headers
// cannot authorize replacing history: that requires the stored completed reply.
func WithClaudeRecoveryRequest(ctx context.Context, h http.Header) (context.Context, error) {
	kind := ""
	for _, key := range []string{"x-cc-compaction-request", "x-claude-code-compaction"} {
		for _, value := range h.Values(key) {
			if value != "manual" && value != "auto" {
				return ctx, ErrRecoveryConflict
			}
			if kind != "" && kind != value {
				return ctx, ErrRecoveryConflict
			}
			kind = value
		}
	}
	class := h.Get("x-claude-code-request-class")
	if (kind != "" && class != "compaction") || (class == "compaction" && kind == "") {
		return ctx, ErrRecoveryConflict
	}
	return context.WithValue(ctx, recoveryCompactionKey{}, recoveryCompactionRequest{kind: kind, infer: class == "" && ExtractCLIVersion(h.Get("User-Agent")) == "2.1.292"}), nil
}

func recoveryCompactionKind(ctx context.Context) string {
	request, _ := ctx.Value(recoveryCompactionKey{}).(recoveryCompactionRequest)
	return request.kind
}

func recoveryLooksLikeCompactionBody(body []byte) bool {
	msgs := gjson.GetBytes(body, "messages").Array()
	if len(msgs) == 0 || msgs[len(msgs)-1].Get("role").String() != "user" {
		return false
	}
	parts, ok := recoveryTextBlocks(json.RawMessage(msgs[len(msgs)-1].Raw))
	if !ok || len(parts) == 0 {
		return false
	}
	text := parts[len(parts)-1]
	return strings.HasPrefix(text, recoveryCompactionPrompt) && strings.Contains(text, "REMINDER: Do NOT call any tools. Respond with plain text only")
}

func recoveryIsCompactionRequest(ctx context.Context, body []byte) bool {
	request, _ := ctx.Value(recoveryCompactionKey{}).(recoveryCompactionRequest)
	return request.kind != "" || (request.infer && recoveryLooksLikeCompactionBody(body))
}

// Custom-origin CLI omits classification headers. Preserve both observed paths:
// the verified compacted prefix, and the ordinary input/reply continuation. A
// user quoting the CLI's complete summary instruction must not lose a valid turn.
func recoveryCompactionContinuation(old, next RecoveryHistory) bool {
	cp := old.Compaction
	if cp == nil || len(cp.ContinuationTail) != 2 || cp.InputPrefix < 0 || cp.InputPrefix > len(old.Messages) {
		return false
	}
	confirmed := old
	confirmed.Compaction = nil
	confirmed.Messages = append(append([]json.RawMessage(nil), old.Messages[:cp.InputPrefix]...), cp.ContinuationTail...)
	return recoveryHistoryExtends(confirmed, next)
}

const recoveryCompactionPrompt = "CRITICAL: Respond with TEXT ONLY. Do NOT call any tools."
const recoveryCompactionIntro = "This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion of the conversation.\n\n"
const recoveryCompactionFollowup = "\nContinue the conversation from where it left off without asking the user any further questions. Resume directly — do not acknowledge the summary, do not recap what was happening, do not preface with \"I'll continue\" or similar. Pick up the last task as if the break never happened."

var recoveryAnalysisRE = regexp.MustCompile(`(?s)<analysis>.*?</analysis>`)
var recoverySummaryRE = regexp.MustCompile(`(?s)<summary>(.*?)</summary>`)
var recoveryBlankLinesRE = regexp.MustCompile(`\n\n+`)

// Qrr in the pinned 2.1.292 bundle removes the first analysis/summary tags,
// collapses blank lines, and trims. It is presentation normalization, not a
// claim that a model summary is semantically lossless.
func recoveryCompactionSummary(text string) string {
	if at := recoveryAnalysisRE.FindStringIndex(text); at != nil {
		text = text[:at[0]] + text[at[1]:]
	}
	if at := recoverySummaryRE.FindStringSubmatchIndex(text); at != nil {
		text = text[:at[0]] + "Summary:\n" + strings.TrimSpace(text[at[2]:at[3]]) + text[at[1]:]
	}
	return strings.TrimSpace(recoveryBlankLinesRE.ReplaceAllString(text, "\n\n"))
}

func recoveryTextBlocks(raw json.RawMessage) ([]string, bool) {
	content := gjson.GetBytes(raw, "content")
	if content.Type == gjson.String {
		return []string{content.String()}, true
	}
	if !content.IsArray() {
		return nil, false
	}
	var out []string
	for _, block := range content.Array() {
		if block.Get("type").String() != "text" {
			return nil, false
		}
		out = append(out, block.Get("text").String())
	}
	return out, true
}

func recoverySameMessage(a, b json.RawMessage, route string) bool {
	return recoveryMessageHash(a, route) == recoveryMessageHash(b, route) &&
		strings.Join(recoveryOpaqueHashes(a), ",") == strings.Join(recoveryOpaqueHashes(b), ",")
}

// Manual compaction may summarize a prefix and retain the most recent assistant
// reply. The native serializer can merge its summary instruction into the last
// user message. Validate every historical byte before accepting that branch.
func recoveryCompactionPrefix(old, next RecoveryHistory) (int, error) {
	if old.Route != "messages" || next.Route != old.Route || old.Model != next.Model ||
		old.ConfigHash != next.ConfigHash || len(next.Messages) == 0 {
		return 0, ErrRecoveryConflict
	}
	last := next.Messages[len(next.Messages)-1]
	if gjson.GetBytes(last, "role").String() != "user" {
		return 0, ErrRecoveryConflict
	}
	parts, ok := recoveryTextBlocks(last)
	if !ok || len(parts) == 0 {
		return 0, ErrRecoveryConflict
	}
	// The summary instruction is always the final text block in captured CLI
	// requests. Custom /compact instructions are inside that block.
	if !strings.HasPrefix(parts[len(parts)-1], recoveryCompactionPrompt) {
		return 0, fmt.Errorf("unverified compaction instruction layout: %w", ErrRecoveryConflict)
	}
	base := next.Messages[:len(next.Messages)-1]
	if len(parts) > 1 {
		blocks := make([]map[string]string, 0, len(parts)-1)
		for i, text := range parts[:len(parts)-1] {
			if i == len(parts)-2 {
				text = strings.TrimSuffix(text, "\n")
			}
			blocks = append(blocks, map[string]string{"type": "text", "text": text})
		}
		raw, e := json.Marshal(map[string]any{"role": "user", "content": blocks})
		if e != nil {
			return 0, e
		}
		base = append(append([]json.RawMessage(nil), base...), raw)
	}
	if len(base) == 0 || len(base) > len(old.Messages) {
		return 0, ErrRecoveryConflict
	}
	for i, message := range base {
		if !recoverySameMessage(old.Messages[i], message, old.Route) {
			return 0, ErrRecoveryConflict
		}
	}
	// Neither the summarized prefix nor the retained suffix may split an
	// outstanding tool call. Never make a compaction request execute a tool.
	prefix := old
	prefix.Messages = old.Messages[:len(base)]
	if !recoveryClosedTools(prefix) || !recoveryClosedTools(old) {
		return 0, ErrRecoveryConflict
	}
	return len(base), nil
}

func recoveryCompactionReply(confirmed RecoveryHistory) (string, error) {
	if len(confirmed.Messages) == 0 {
		return "", ErrRecoveryUncertain
	}
	parts, ok := recoveryTextBlocks(confirmed.Messages[len(confirmed.Messages)-1])
	if !ok {
		return "", ErrRecoveryUncertain
	}
	summary := recoveryCompactionSummary(strings.Join(parts, ""))
	if summary == "" {
		return "", ErrRecoveryUncertain
	}
	return summary, nil
}

// Only permit the known CLI wrapper after the exact recorded summary. A local
// transcript path is display data, never a server-side file reference.
func recoveryCompactionWrapper(text, summary string) bool {
	prefix := recoveryCompactionIntro + summary
	if !strings.HasPrefix(text, prefix) {
		return false
	}
	tail := strings.TrimSuffix(strings.TrimPrefix(text, prefix), "\n")
	if tail == "" || tail == recoveryCompactionFollowup {
		return true
	}
	const transcript = "\n\nIf you need specific details from before compaction (like exact code snippets, error messages, or content you generated), read the full transcript at: "
	if strings.HasPrefix(tail, transcript) {
		tail = strings.TrimPrefix(tail, transcript)
		at := strings.IndexByte(tail, '\n')
		if at < 0 {
			return tail != "" && !strings.ContainsAny(tail, "\r\x00")
		}
		if at == 0 || strings.ContainsAny(tail[:at], "\r\x00") {
			return false
		}
		tail = tail[at:]
	}
	tail = strings.TrimPrefix(tail, "\n\nRecent messages are preserved verbatim.")
	return tail == "" || tail == recoveryCompactionFollowup
}

func recoveryAcceptCompaction(old, next RecoveryHistory) bool {
	cp := old.Compaction
	if cp == nil || cp.Prefix < 1 || cp.Prefix > len(old.Messages) || old.Route != "messages" ||
		next.Route != old.Route || old.Model != next.Model || old.ConfigHash != next.ConfigHash || len(next.Messages) == 0 {
		return false
	}
	first := next.Messages[0]
	if gjson.GetBytes(first, "role").String() != "user" {
		return false
	}
	content := gjson.GetBytes(first, "content")
	firstText := ""
	if content.Type == gjson.String {
		firstText = content.String()
	} else if content.IsArray() && len(content.Array()) > 0 && content.Array()[0].Get("type").String() == "text" {
		firstText = content.Array()[0].Get("text").String()
	}
	if len(recoveryOpaqueHashes(first)) > 0 || !recoveryCompactionWrapper(firstText, cp.Summary) {
		return false
	}
	index := 1
	// 5.5 reinserts its current system control frame after the summary. It must
	// match a system frame already observed in this same authenticated history.
	for index < len(next.Messages) && gjson.GetBytes(next.Messages[index], "role").String() == "system" {
		matched := false
		for _, known := range old.Messages {
			if gjson.GetBytes(known, "role").String() == "system" && recoverySameMessage(known, next.Messages[index], old.Route) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
		index++
	}
	for _, retained := range old.Messages[cp.Prefix:] {
		if index >= len(next.Messages) || !recoverySameMessage(retained, next.Messages[index], old.Route) {
			return false
		}
		index++
	}
	// New user instructions are allowed as usual. New assistant/tool/signed
	// history cannot be introduced under the guise of a compaction marker.
	for _, message := range next.Messages[index:] {
		if gjson.GetBytes(message, "role").String() != "user" || len(recoveryOpaqueHashes(message)) > 0 {
			return false
		}
	}
	return recoveryClosedTools(next)
}
