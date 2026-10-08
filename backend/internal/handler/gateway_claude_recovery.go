package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const recoveryExchangeKey = "sub2apiManagedRecoveryExchange"
const recoveryCaptureKey = "sub2apiManagedRecoveryCapture"

// Preparation runs after the existing authentication, model policy, moderation
// and billing checks, but before account selection. Strict requests are untouched.
func (h *GatewayHandler) prepareClaudeRecovery(c *gin.Context, parsed *service.ParsedRequest, body *[]byte, route string, readOnly bool) bool {
	if h.gatewayService == nil {
		return true
	}
	m := h.gatewayService.ClaudeRecovery()
	if m == nil {
		return true
	}
	key, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || key.GroupID == nil || *key.GroupID <= 0 {
		return true
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID != key.UserID {
		h.errorResponse(c, 401, "authentication_error", "Invalid recovery identity")
		return false
	}
	sid, idErr := service.ResolveClaudeRecoveryClientID(c.Request.Context(), c, *body)
	if idErr != nil {
		h.errorResponse(c, 400, "invalid_request_error", "Conflicting or invalid conversation identifiers")
		return false
	}
	allowed := m.Allowed(*key.GroupID)
	if sid == "" {
		if !allowed {
			return true
		}
		sid = uuid.NewString()
	}
	id, e := uuid.Parse(sid)
	if e != nil || id == uuid.Nil {
		if !allowed {
			return true
		}
		h.errorResponse(c, 400, "invalid_request_error", "Conversation identifier must be a UUID")
		return false
	}
	sid = id.String()
	scope := service.RecoveryScope{UserID: key.UserID, GroupID: *key.GroupID, ClientSession: sid}
	if !allowed {
		exists, err := m.Existing(c.Request.Context(), scope)
		if err != nil || exists {
			h.errorResponse(c, 409, "invalid_request_error", "Managed conversation cannot fall back to strict routing; start a new conversation")
			return false
		}
		return true
	}
	if c.GetHeader("X-Sub2API-Recovery-Purpose") != "" {
		h.errorResponse(c, 409, "invalid_request_error", "Summary requests must use a group with managed recovery disabled")
		return false
	}
	if effectiveAPIKeyPlatform(c, key) != service.PlatformAnthropic {
		h.errorResponse(c, 400, "invalid_request_error", "Managed recovery requires an Anthropic group")
		return false
	}
	if version := service.ExtractCLIVersion(c.GetHeader("User-Agent")); version != "" && version != "2.1.292" {
		h.errorResponse(c, 400, "invalid_request_error", "Unverified CLI version for managed recovery")
		return false
	}
	for _, name := range []string{"x-claude-remote-session-id", "x-claude-code-agent-id", "x-claude-code-parent-agent-id"} {
		if c.GetHeader(name) != "" {
			h.errorResponse(c, 409, "invalid_request_error", "Branched or remote conversations require a separately verified recovery profile")
			return false
		}
	}
	if c.GetHeader("x-claude-code-request-class") == "auxiliary" && !readOnly {
		h.errorResponse(c, 409, "invalid_request_error", "Auxiliary generation requires a separate conversation in managed mode")
		return false
	}
	// Earlier queue heartbeats may already have committed an SSE response. Never
	// hide a migration behind such a response or change its identity mid-stream.
	if c.Writer.Written() {
		h.handleStreamingAwareError(c, 409, "api_error", "Recovery must be prepared before response output", true)
		return false
	}
	c.Header("X-Sub2API-Session-Id", sid)
	recoveryCtx, err := service.WithClaudeRecoveryRequest(c.Request.Context(), c.Request.Header)
	if err != nil {
		h.errorResponse(c, 409, "invalid_request_error", "Conflicting or unverified compaction markers")
		return false
	}
	exchange, err := m.Begin(recoveryCtx, h.gatewayService, scope, *body, route, c.GetHeader("Idempotency-Key"), readOnly)
	if err != nil {
		status := http.StatusServiceUnavailable
		message := "Managed recovery unavailable; retry later or start a new conversation"
		switch {
		case errors.Is(err, service.ErrRecoveryBusy):
			status = 409
			message = err.Error()
		case errors.Is(err, service.ErrRecoveryConflict):
			status = 409
			message = "Conversation history or identity conflicts with recovery state"
		case errors.Is(err, service.ErrRecoveryUncertain):
			status = 409
			message = err.Error()
		case errors.Is(err, service.ErrRecoveryExpired):
			status = 410
			message = err.Error()
		}
		h.errorResponse(c, status, "api_error", message)
		return false
	}
	if exchange.Replay != nil {
		for name, values := range exchange.Replay.Headers {
			if strings.EqualFold(name, "Content-Type") {
				c.Writer.Header()[name] = values
			}
		}
		c.Header("X-Sub2API-Session-Id", sid)
		c.Data(exchange.Replay.Status, exchange.Replay.Headers.Get("Content-Type"), exchange.Replay.Body)
		return false
	}
	if err = parsed.ReplaceBody(exchange.Body); err != nil {
		exchange.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exchange.Finish(ctx, 400, nil, nil, false, "")
		h.errorResponse(c, 400, "invalid_request_error", "Invalid recovered request")
		return false
	}
	*body = exchange.Body
	req := c.Request.Clone(exchange.Context(c.Request.Context()))
	req.Body = io.NopCloser(bytes.NewReader(*body))
	req.ContentLength = int64(len(*body))
	req.Header.Del("X-Sub2API-Session-Id")
	req.Header.Set("X-Claude-Code-Session-Id", exchange.Row.Session)
	c.Request = req
	capture := &claudeRecoveryWriter{ResponseWriter: c.Writer, headers: c.Writer.Header().Clone(), status: 200, size: -1, hold: !parsed.Stream || readOnly, session: sid, exchange: exchange}
	c.Writer = capture
	c.Set(recoveryExchangeKey, exchange)
	c.Set(recoveryCaptureKey, capture)
	return true
}

func (h *GatewayHandler) finishClaudeRecovery(c *gin.Context, originalRequest *http.Request, originalWriter gin.ResponseWriter) {
	defer func() { c.Request = originalRequest; c.Writer = originalWriter }()
	value, ok := c.Get(recoveryExchangeKey)
	if !ok {
		return
	}
	p, valid := value.(*service.ClaudeRecoveryExchange)
	if !valid || p == nil {
		return
	}
	captureValue, ok := c.Get(recoveryCaptureKey)
	if !ok {
		p.Close()
		return
	}
	w, valid := captureValue.(*claudeRecoveryWriter)
	if !valid || w == nil {
		p.Close()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	complete := w.writeErr == nil && !w.overflow
	if gjson.GetBytes(p.Body, "stream").Bool() {
		complete = complete && w.terminal
	} else {
		complete = complete && json.Valid(w.buffer.Bytes())
	}
	replay := w.buffer.Bytes()
	if w.overflow {
		replay = nil
	}
	err := p.Finish(ctx, w.status, w.headers, replay, complete, originalRequest.Header.Get("Idempotency-Key"))
	if err != nil && !w.committed {
		c.Writer = originalWriter
		h.errorResponse(c, 503, "api_error", "Unable to persist recovery state; request outcome may be uncertain")
		return
	}
	if !w.committed {
		_ = w.publish()
	}
}

// Error/non-stream responses are held until the durable operation is finalized.
// Successful streams remain streams; incomplete output makes recovery uncertain.
type claudeRecoveryWriter struct {
	gin.ResponseWriter
	headers                             http.Header
	status, size                        int
	hold, committed, overflow, terminal bool
	session                             string
	buffer                              bytes.Buffer
	tail                                string
	writeErr                            error
	exchange                            *service.ClaudeRecoveryExchange
}

func (w *claudeRecoveryWriter) Header() http.Header { return w.headers }
func (w *claudeRecoveryWriter) WriteHeader(code int) {
	if w.size >= 0 {
		return
	}
	w.status = code
}
func (w *claudeRecoveryWriter) WriteHeaderNow() {
	if w.size < 0 {
		w.size = 0
	}
}
func (w *claudeRecoveryWriter) Status() int                       { return w.status }
func (w *claudeRecoveryWriter) Size() int                         { return w.size }
func (w *claudeRecoveryWriter) Written() bool                     { return w.size >= 0 }
func (w *claudeRecoveryWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *claudeRecoveryWriter) Write(data []byte) (int, error) {
	if w.exchange.LeaseLost.Load() {
		w.writeErr = service.ErrRecoveryConflict
		return 0, w.writeErr
	}
	w.WriteHeaderNow()
	w.size += len(data)
	if w.buffer.Len()+len(data) <= 2*1024*1024 {
		_, _ = w.buffer.Write(data)
	} else {
		w.overflow = true
	}
	w.tail += string(data)
	for _, line := range strings.Split(w.tail, "\n") {
		if strings.TrimSpace(line) == "event: message_stop" || strings.TrimSpace(line) == "data: [DONE]" {
			w.terminal = true
		}
		if strings.HasPrefix(line, "data: ") {
			raw := []byte(strings.TrimPrefix(line, "data: "))
			if gjson.ValidBytes(raw) {
				kind := gjson.GetBytes(raw, "type").String()
				if kind == "message_stop" || (kind == "response.completed" && gjson.GetBytes(raw, "response.status").String() == "completed") {
					w.terminal = true
				}
			}
		}
	}
	if len(w.tail) > 8192 {
		w.tail = w.tail[len(w.tail)-8192:]
	}
	if w.hold || w.status >= 400 {
		return len(data), nil
	}
	if !w.committed {
		w.copyHeaders()
		w.ResponseWriter.WriteHeader(w.status)
		w.committed = true
	}
	n, e := w.ResponseWriter.Write(data)
	if e != nil {
		w.writeErr = e
	}
	return n, e
}
func (w *claudeRecoveryWriter) copyHeaders() {
	for k := range w.ResponseWriter.Header() {
		delete(w.ResponseWriter.Header(), k)
	}
	for k, v := range w.headers {
		w.ResponseWriter.Header()[k] = append([]string(nil), v...)
	}
	w.ResponseWriter.Header().Set("X-Sub2API-Session-Id", w.session)
	if w.ResponseWriter.Header().Get("X-Claude-Code-Session-Id") != "" {
		w.ResponseWriter.Header().Set("X-Claude-Code-Session-Id", w.session)
	}
	w.ResponseWriter.Header().Add("Access-Control-Expose-Headers", "X-Sub2API-Session-Id")
}
func (w *claudeRecoveryWriter) publish() error {
	if w.committed {
		return nil
	}
	if w.overflow {
		return service.ErrRecoveryConflict
	}
	w.copyHeaders()
	w.ResponseWriter.WriteHeader(w.status)
	w.committed = true
	_, e := w.ResponseWriter.Write(w.buffer.Bytes())
	return e
}
func (w *claudeRecoveryWriter) Flush() {
	w.WriteHeaderNow()
	if w.hold || w.status >= 400 {
		return
	}
	if !w.committed {
		w.copyHeaders()
		w.ResponseWriter.WriteHeader(w.status)
		w.committed = true
	}
	w.ResponseWriter.Flush()
}
