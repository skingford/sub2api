package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func recoveryRewriteAttribution(body []byte) ([]byte, error) {
	for i, block := range gjson.GetBytes(body, "system").Array() {
		text := block.Get("text").String()
		if !strings.HasPrefix(text, "x-anthropic-billing-header:") {
			continue
		}
		version := claudeCompatibilityVersion
		if strings.TrimPrefix(ccVersionInBillingRe.FindString(text), "cc_version=") == "2.1.295" {
			version = "2.1.295"
		}
		version = claudeCompatibilityModelVersion(version, gjson.GetBytes(body, "model").String())
		replacement := "cc_version=" + version + "." + computeClaudeCodeFingerprint(body, version)
		if ccVersionWithFingerprintInBillingRe.MatchString(text) {
			text = ccVersionWithFingerprintInBillingRe.ReplaceAllString(text, replacement)
		} else {
			text = ccVersionInBillingRe.ReplaceAllString(text, replacement)
		}
		var e error
		body, e = sjson.SetBytes(body, fmt.Sprintf("system.%d.text", i), text)
		if e != nil {
			return nil, e
		}
	}
	return body, nil
}

// Count the actual restored payload on its bound account, under the parent's
// lease, before dispatching a model operation. A count cannot execute tools.
// This is separate from the parent's sent marker and does not recurse.
func (s *GatewayService) recoveryPreflightCount(parent *http.Request, proxy string, a *Account, profile *tlsfingerprint.Profile, p *ClaudeRecoveryExchange) (*http.Response, error) {
	if p.ReadOnly || len(p.Row.Restore) == 0 {
		return nil, nil
	}
	if parent.GetBody == nil {
		return nil, fmt.Errorf("managed request cannot be counted safely")
	}
	source, e := parent.GetBody()
	if e != nil {
		return nil, e
	}
	raw, e := io.ReadAll(io.LimitReader(source, int64(p.manager.cfg.MaxHistoryBytes)+1))
	_ = source.Close()
	if e != nil || len(raw) > p.manager.cfg.MaxHistoryBytes {
		return nil, fmt.Errorf("managed request exceeds the configured size limit")
	}
	payload := map[string]json.RawMessage{}
	for _, key := range []string{"model", "system", "messages", "tools"} {
		v := gjson.GetBytes(raw, key)
		if v.Exists() {
			payload[key] = json.RawMessage(v.Raw)
		}
	}
	body, e := json.Marshal(payload)
	if e != nil {
		return nil, e
	}
	endpoint := *parent.URL
	if !strings.HasSuffix(endpoint.Path, "/messages") {
		return nil, fmt.Errorf("unverified managed count endpoint")
	}
	endpoint.Path += "/count_tokens"
	endpoint.RawPath = ""
	child := &ClaudeRecoveryExchange{Row: p.Row, History: p.History, ReadOnly: true, manager: p.manager}
	ctx := child.Context(parent.Context())
	ctx = context.WithValue(ctx, nativeClaudeBodyDigestKey{}, false)
	// #nosec G704 -- parent is an already validated outgoing request; only a fixed same-origin path suffix is appended.
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	req.Header = parent.Header.Clone()
	for _, key := range []string{"Content-Length", "Transfer-Encoding", "X-Stainless-Timeout", "X-Stainless-Helper-Method", "x-claude-code-prompt-id"} {
		deleteHeaderAllForms(req.Header, key)
	}
	setHeaderRaw(req.Header, "x-client-request-id", uuid.NewString())
	setHeaderRaw(req.Header, "x-claude-code-request-class", "auxiliary")
	var betas []string
	for _, beta := range claude2292CompatibilityBetas(p.History.Model, body, true) {
		if beta == claude.BetaTokenCounting || containsBetaToken(getHeaderRaw(parent.Header, "anthropic-beta"), beta) {
			betas = append(betas, beta)
		}
	}
	setHeaderRaw(req.Header, "anthropic-beta", strings.Join(betas, ","))
	req = req.WithContext(withNativeClaudeBodyIntegrity(req.Context(), nil, a, body))
	body, e = finalizeNativeClaudeRequest(req, nil, a, body)
	if e != nil {
		return nil, e
	}
	if e = s.bindClaudeConversation(req.Context(), nil, a, body, req.Header); e != nil {
		return nil, e
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	prepareNativeClaudeTransport(req, nil, a, body)
	resp, e := s.doClaudeHTTP(req, proxy, a, profile)
	if e != nil {
		return nil, e
	}
	if resp.StatusCode >= 400 {
		p.ResponseStatus.Store(int64(resp.StatusCode))
		return resp, nil
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, 64*1024+1))
	_ = resp.Body.Close()
	count := gjson.GetBytes(data, "input_tokens")
	if e != nil || len(data) > 64*1024 || !gjson.ValidBytes(data) || count.Type != gjson.Number || count.Int() < 0 {
		return nil, fmt.Errorf("managed token count is invalid")
	}
	limit := int64(p.manager.cfg.ContextWindowTokens)
	output := gjson.GetBytes(raw, "max_tokens").Int()
	if count.Int() > limit || output < 1 || output > limit-count.Int() {
		p.ResponseStatus.Store(http.StatusRequestEntityTooLarge)
		return &http.Response{StatusCode: 413, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"invalid_request_error","message":"Restored context exceeds the configured token budget; no model request was sent"}}`))}, nil
	}
	return nil, nil
}
