package requesttrace

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptrace"
	"sync"
)

type upstreamKey struct{}
type attemptKey struct{}

// WithUpstream annotates a request copy; it adds no on-wire headers.
func WithUpstream(req *http.Request, accountID int64, concurrency int, proxy, profile string) *http.Request {
	if req == nil || FromContext(req.Context()) == nil {
		return req
	}
	fields := map[string]any{"account_id": accountID, "account_concurrency": concurrency, "proxy": Proxy(proxy), "transport_profile": profile}
	FromContext(req.Context()).Event("upstream.dispatch", fields)
	return req.WithContext(context.WithValue(req.Context(), upstreamKey{}, fields))
}

func DispatchError(req *http.Request, err error) {
	if req == nil || err == nil {
		return
	}
	t := FromContext(req.Context())
	fields, _ := req.Context().Value(upstreamKey{}).(map[string]any)
	t.Event("upstream.dispatch_error", map[string]any{"upstream": fields, "error": SafeError(err)})
}

func InstrumentClient(client *http.Client, req *http.Request) *http.Client {
	if req == nil || FromContext(req.Context()) == nil {
		return client
	}
	if client == nil {
		client = http.DefaultClient
	}
	c := *client
	base := c.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	c.Transport = &transport{base: base}
	return &c
}

type transport struct{ base http.RoundTripper }

type responseBody struct {
	io.ReadCloser
	trace    *Trace
	attempt  int64
	response *http.Response
	once     sync.Once
}

func (b *responseBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() {
		b.trace.Event("upstream.response_closed", map[string]any{"attempt": b.attempt, "trailers": Headers(b.response.Trailer), "error": SafeError(err)})
	})
	return err
}

// CaptureDecodedResponse preserves the readable response as well as the raw
// compressed body when the gateway explicitly decompresses a response.
func CaptureDecodedResponse(resp *http.Response, raw io.ReadCloser) {
	if resp == nil || resp.Body == raw {
		return
	}
	b, ok := raw.(*responseBody)
	if !ok {
		return
	}
	b.trace.Event("upstream.decoded_response", map[string]any{"attempt": b.attempt, "headers": Headers(resp.Header)})
	resp.Body = CaptureBody(resp.Body, b.trace.Stream("upstream.response_decoded", b.attempt, -1))
}

func (rt *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	t := FromContext(req.Context())
	if t == nil {
		return rt.base.RoundTrip(req)
	}
	attempt := t.attempts.Add(1)
	upstream, _ := req.Context().Value(upstreamKey{}).(map[string]any)
	t.Event("upstream.request", map[string]any{
		"attempt": attempt, "upstream": upstream, "method": req.Method, "url": URL(req.URL),
		"host": req.Host, "headers": Headers(req.Header), "content_length": req.ContentLength,
		"transfer_encoding": req.TransferEncoding,
	})
	ctx := context.WithValue(req.Context(), attemptKey{}, attempt)
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			t.Event("upstream.wrote_request", map[string]any{"attempt": attempt, "error": SafeError(info.Err)})
		},
		DNSDone: func(info httptrace.DNSDoneInfo) {
			t.Event("upstream.dns", map[string]any{"attempt": attempt, "addresses": info.Addrs, "coalesced": info.Coalesced, "error": SafeError(info.Err)})
		},
		ConnectDone: func(network, addr string, err error) {
			t.Event("upstream.connect", map[string]any{"attempt": attempt, "network": network, "address": addr, "error": SafeError(err)})
		},
		GotConn: func(info httptrace.GotConnInfo) {
			t.Event("upstream.connection", map[string]any{"attempt": attempt, "reused": info.Reused, "was_idle": info.WasIdle, "idle_ms": info.IdleTime.Milliseconds(), "local": info.Conn.LocalAddr().String(), "remote": info.Conn.RemoteAddr().String()})
		},
		TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			t.Event("upstream.tls", map[string]any{"attempt": attempt, "version": state.Version, "cipher_suite": state.CipherSuite, "alpn": state.NegotiatedProtocol, "server_name": state.ServerName, "resumed": state.DidResume, "error": SafeError(err)})
		},
		GotFirstResponseByte: func() { t.Event("upstream.first_byte", map[string]any{"attempt": attempt}) },
	})
	copyReq := req.WithContext(ctx)
	expected := req.ContentLength
	if expected == 0 && req.Body != nil && req.Body != http.NoBody {
		expected = -1
	}
	stream := t.Stream("upstream.request", attempt, expected)
	copyReq.Body = CaptureBody(req.Body, stream)
	resp, err := rt.base.RoundTrip(copyReq)
	if err != nil {
		stream.Finish("roundtrip_error", err)
		t.Event("upstream.error", map[string]any{"attempt": attempt, "error": SafeError(err)})
		return resp, err
	}
	t.Event("upstream.response", map[string]any{
		"attempt": attempt, "status": resp.StatusCode, "protocol": resp.Proto,
		"headers": Headers(resp.Header), "content_length": resp.ContentLength,
		"transfer_encoding": resp.TransferEncoding, "transport_uncompressed": resp.Uncompressed,
	})
	resp.Body = &responseBody{ReadCloser: CaptureBody(resp.Body, t.Stream("upstream.response", attempt, resp.ContentLength)), trace: t, attempt: attempt, response: resp}
	return resp, nil
}

// NativeHeaders records the native transport's effective application headers
// and explicit header order, without claiming to be a packet capture.
func NativeHeaders(req *http.Request, headers http.Header) {
	if req == nil {
		return
	}
	t := FromContext(req.Context())
	attempt, _ := req.Context().Value(attemptKey{}).(int64)
	t.Event("upstream.native_headers", map[string]any{"attempt": attempt, "headers": Headers(headers)})
}
