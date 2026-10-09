package requesttrace

import (
	"context"
	"net/http/httptrace"

	ftrace "github.com/bogdanfinn/fhttp/httptrace"
)

// fhttp has its own context keys. Bridge its connection and first-byte hooks
// so Claude's native transport has the same evidence as net/http. The custom
// TLS dialer is kept intact; no synthetic TLS handshake state is invented.
func NativeContext(ctx context.Context) context.Context {
	if FromContext(ctx) == nil {
		return ctx
	}
	trace := httptrace.ContextClientTrace(ctx)
	if trace == nil {
		return ctx
	}
	return ftrace.WithClientTrace(ctx, &ftrace.ClientTrace{
		GotConn: func(info ftrace.GotConnInfo) {
			if trace.GotConn != nil {
				trace.GotConn(httptrace.GotConnInfo{Conn: info.Conn, Reused: info.Reused, WasIdle: info.WasIdle, IdleTime: info.IdleTime})
			}
		},
		GotFirstResponseByte: trace.GotFirstResponseByte,
		WroteRequest: func(info ftrace.WroteRequestInfo) {
			if trace.WroteRequest != nil {
				trace.WroteRequest(httptrace.WroteRequestInfo{Err: info.Err})
			}
		},
	})
}
