package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/tidwall/gjson"
)

// fingerprintSalt 是计算 cc_version 后缀指纹的盐值。
//
// Legacy compatibility value inherited from Parrot's FINGERPRINT_SALT.
// Client-side matching does not establish any server-side detection rule.
const fingerprintSalt = "59cf53e54c78"

// computeClaudeCodeFingerprint builds the legacy compatibility cc_version suffix:
//
//  1. 取 messages 中第一条 role=user 的纯文本（首块 text）
//  2. 取该文本的第 4、7、20 字符（不足以 '0' 补齐）
//  3. SHA256(SALT + chars + cc_version) 取 hex 前 3 字符
//
// This byte-indexed helper differs from native CLI behavior for Unicode and
// requests with inserted system reminders. Do not use it to refresh an already
// matching native attribution block. It is not a verified server credential.
func computeClaudeCodeFingerprint(body []byte, version string) string {
	firstText := extractFirstUserText(body)
	indices := []int{4, 7, 20}
	chars := make([]byte, 0, 3)
	for _, i := range indices {
		if i < len(firstText) {
			chars = append(chars, firstText[i])
		} else {
			chars = append(chars, '0')
		}
	}
	sum := sha256.Sum256([]byte(fingerprintSalt + string(chars) + version))
	return hex.EncodeToString(sum[:])[:3]
}

// extractFirstUserText 提取 messages 中第一条 user 消息的首段 text 内容。
// 兼容 string 和 []block 两种 content 格式。
func extractFirstUserText(body []byte) string {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return ""
	}
	first := ""
	messages.ForEach(func(_, msg gjson.Result) bool {
		if msg.Get("role").String() != "user" {
			return true
		}
		content := msg.Get("content")
		if content.Type == gjson.String {
			first = content.String()
			return false
		}
		if content.IsArray() {
			content.ForEach(func(_, block gjson.Result) bool {
				if block.Get("type").String() == "text" {
					first = block.Get("text").String()
					return false
				}
				return true
			})
			return false
		}
		return false
	})
	return first
}

// buildBillingAttributionText 构造 system 数组的 billing attribution 文本。
//
// 形态对齐真实 Claude Code CLI：
//
//	x-anthropic-billing-header: cc_version=2.1.161.{fp}; cc_entrypoint=cli;
//
// This compatibility block omits cch. That is not a universal native-CLI rule:
// 2.1.286's attribution builder includes a placeholder on its first-party and
// Vertex branches, but omits it for a custom base URL. A localhost capture
// cannot establish the final direct-origin value or server validation rules.
// See docs/claude-code-request-parity.md for the evidence boundary.
//
// 此 block 不带 cache_control（与真实 CLI 一致；cache breakpoint 由后续的
// Claude Code prompt block 承担）。
func buildBillingAttributionText(body []byte, cliVersion string) (string, error) {
	if cliVersion == "" {
		return "", fmt.Errorf("cliVersion required")
	}
	fp := computeClaudeCodeFingerprint(body, cliVersion)
	return fmt.Sprintf(
		"x-anthropic-billing-header: cc_version=%s.%s; cc_entrypoint=cli;",
		cliVersion, fp,
	), nil
}
