package middleware

import (
	"bufio"
	"log/slog"
	"net"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttrace"
	"github.com/gin-gonic/gin"
)

// NewRequestTrace is scoped to gateway routes, never panel/login/OAuth routes.
func NewRequestTrace(cfg config.RequestTraceConfig) gin.HandlerFunc {
	recorder, err := requesttrace.New(cfg)
	if err != nil {
		// Do not silently run with requested forensic logging unavailable.
		panic("initialize gateway request trace: " + err.Error())
	}
	if recorder != nil {
		slog.Info("gateway full request tracing enabled", "directory", cfg.Directory, "max_body_bytes", cfg.MaxBodyBytes, "max_size_mb", cfg.MaxSizeMB, "max_backups", cfg.MaxBackups, "max_age_days", cfg.MaxAgeDays)
	}
	return RequestTrace(recorder)
}

func RequestTrace(recorder *requesttrace.Recorder) gin.HandlerFunc {
	return func(c *gin.Context) {
		if recorder == nil {
			c.Next()
			return
		}
		ctx, trace := recorder.Start(c.Request.Context())
		c.Request = c.Request.WithContext(ctx)
		c.Header("X-Sub2api-Trace-ID", trace.ID)
		trace.Event("request.start", map[string]any{
			"request_id": ctx.Value(ctxkey.RequestID), "client_request_id": ctx.Value(ctxkey.ClientRequestID),
			"method": c.Request.Method, "url": requesttrace.URL(c.Request.URL), "host": c.Request.Host,
			"route": c.FullPath(), "headers": requesttrace.Headers(c.Request.Header),
			"protocol": c.Request.Proto, "remote_addr": c.Request.RemoteAddr, "client_ip": ip.GetClientIP(c),
			"content_length": c.Request.ContentLength,
		})
		input := trace.Stream("client.request", 0, c.Request.ContentLength)
		c.Request.Body = requesttrace.CaptureBody(c.Request.Body, input)
		writer := &traceResponseWriter{ResponseWriter: c.Writer, trace: trace, body: trace.Stream("client.response", 0, -1)}
		c.Writer = writer
		returned := false
		defer func() {
			input.Finish("handler_stopped_reading", nil)
			if returned && !writer.hijacked {
				writer.body.Finish("handler_return", writer.writeErr)
			} else {
				writer.body.Finish("handler_interrupted_or_hijacked", writer.writeErr)
			}
			ctx := c.Request.Context()
			fields := map[string]any{
				"request_id": ctx.Value(ctxkey.RequestID), "client_request_id": ctx.Value(ctxkey.ClientRequestID),
				"account_id": ctx.Value(ctxkey.AccountID), "platform": ctx.Value(ctxkey.Platform), "model": ctx.Value(ctxkey.Model),
				"status": c.Writer.Status(), "headers": requesttrace.Headers(c.Writer.Header()),
				"handler_returned": returned, "hijacked": writer.hijacked, "context_error": requesttrace.SafeError(ctx.Err()),
			}
			if key, ok := GetAPIKeyFromContext(c); ok && key != nil {
				fields["api_key_id"], fields["user_id"], fields["group_id"] = key.ID, key.UserID, key.GroupID
			}
			if reason, rejected := GetIngressRejectReason(c); rejected {
				fields["ingress_reject_reason"] = reason
			}
			trace.Event("request.end", fields)
		}()
		c.Next()
		returned = true
	}
}

type traceResponseWriter struct {
	gin.ResponseWriter
	trace                    *requesttrace.Trace
	body                     *requesttrace.Stream
	headersWritten, hijacked bool
	writeErr                 error
}

func (w *traceResponseWriter) recordHeaders() {
	if !w.headersWritten {
		w.headersWritten = true
		w.trace.Event("client.response", map[string]any{"status": w.Status(), "headers": requesttrace.Headers(w.Header())})
	}
}

func (w *traceResponseWriter) WriteHeaderNow() { w.ResponseWriter.WriteHeaderNow(); w.recordHeaders() }

func (w *traceResponseWriter) Write(p []byte) (int, error) {
	w.WriteHeaderNow()
	n, err := w.ResponseWriter.Write(p)
	w.body.Write(p[:n])
	if err != nil {
		w.writeErr = err
	}
	return n, err
}

func (w *traceResponseWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

func (w *traceResponseWriter) Flush() { w.WriteHeaderNow(); w.ResponseWriter.Flush() }

func (w *traceResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := w.ResponseWriter.Hijack()
	if err == nil {
		w.hijacked = true
		w.trace.Event("client.hijacked", map[string]any{"coverage": "HTTP only; websocket frames are not captured"})
	}
	return conn, rw, err
}

var _ http.Flusher = (*traceResponseWriter)(nil)
