package repository

import (
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttrace"
	fhttp "github.com/bogdanfinn/fhttp"
)

// nativeClaudeHTTP1Transport retains a pooled HTTP implementation while allowing
// request header order to be controlled. TLS dialing and certificate validation
// come from the existing transport; only the scoped native CLI profile uses it.
type nativeClaudeHTTP1Transport struct {
	transport *fhttp.Transport
}

func newNativeClaudeHTTP1Transport(base *http.Transport) *nativeClaudeHTTP1Transport {
	t := &nativeClaudeHTTP1Transport{transport: &fhttp.Transport{
		DialContext: base.DialContext, DialTLSContext: base.DialTLSContext,
		MaxIdleConns: base.MaxIdleConns, MaxIdleConnsPerHost: base.MaxIdleConnsPerHost,
		MaxConnsPerHost: base.MaxConnsPerHost, IdleConnTimeout: base.IdleConnTimeout,
		ResponseHeaderTimeout: base.ResponseHeaderTimeout, TLSHandshakeTimeout: base.TLSHandshakeTimeout,
		ExpectContinueTimeout: base.ExpectContinueTimeout, DisableCompression: true,
		ForceAttemptHTTP2: false,
	}}
	if base.Proxy != nil {
		t.transport.Proxy = func(req *fhttp.Request) (*url.URL, error) {
			return base.Proxy((&http.Request{Method: req.Method, URL: req.URL, Host: req.Host,
				Header: http.Header(req.Header)}).WithContext(req.Context()))
		}
	}
	return t
}

func (t *nativeClaudeHTTP1Transport) CloseIdleConnections() { t.transport.CloseIdleConnections() }

func (t *nativeClaudeHTTP1Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	fr := (&fhttp.Request{
		Method: req.Method, URL: req.URL, Proto: req.Proto, ProtoMajor: req.ProtoMajor, ProtoMinor: req.ProtoMinor,
		Header: nativeClaudeHeaders(req), Body: req.Body, GetBody: req.GetBody,
		ContentLength: req.ContentLength, TransferEncoding: append([]string(nil), req.TransferEncoding...),
		Close: req.Close, Host: req.Host, Trailer: fhttp.Header(req.Trailer.Clone()),
	}).WithContext(requesttrace.NativeContext(req.Context()))
	if req.GetBody != nil {
		fr.GetBody = func() (io.ReadCloser, error) {
			body, err := req.GetBody()
			if body == http.NoBody {
				return fhttp.NoBody, err
			}
			return body, err
		}
	}
	if req.Body == http.NoBody {
		fr.Body = fhttp.NoBody
	}
	requesttrace.NativeHeaders(req, http.Header(fr.Header))
	r, err := t.transport.RoundTrip(fr)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		Status: r.Status, StatusCode: r.StatusCode, Proto: r.Proto, ProtoMajor: r.ProtoMajor, ProtoMinor: r.ProtoMinor,
		Header: http.Header(r.Header), Body: r.Body, ContentLength: r.ContentLength,
		TransferEncoding: append([]string(nil), r.TransferEncoding...), Close: r.Close,
		Uncompressed: r.Uncompressed, Trailer: http.Header(r.Trailer), Request: req,
	}, nil
}

// Native 2.1.292 sorts its application headers by their preserved wire spelling,
// then appends these four HTTP/1.1 transport fields. Values are never copied from
// the captures: credentials, request IDs and body length remain request-specific.
func nativeClaudeHeaders(req *http.Request) fhttp.Header {
	h := make(fhttp.Header, len(req.Header)+4)
	for key, values := range req.Header {
		lower := strings.ToLower(key)
		if lower == "host" || lower == "content-length" || lower == "connection" ||
			lower == strings.ToLower(fhttp.HeaderOrderKey) || lower == strings.ToLower(fhttp.PHeaderOrderKey) {
			continue
		}
		wireKey := key
		switch lower {
		case "accept", "authorization", "content-type", "content-encoding", "user-agent", "accept-encoding":
			wireKey = http.CanonicalHeaderKey(lower)
		}
		h[wireKey] = append(h[wireKey], values...)
	}
	if !req.Close {
		h["Connection"] = []string{"keep-alive"}
	}
	if h.Get("Accept-Encoding") == "" {
		h["Accept-Encoding"] = []string{"gzip, deflate, br, zstd"}
	}
	var keys []string
	for key := range h {
		switch strings.ToLower(key) {
		case "content-encoding", "connection", "host", "accept-encoding", "content-length":
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		h[fhttp.HeaderOrderKey] = append(h[fhttp.HeaderOrderKey], strings.ToLower(key))
	}
	// Bun adds request compression after sorting application headers. The
	// native gzip capture places Content-Encoding immediately before Connection.
	if _, exists := h["Content-Encoding"]; exists {
		h[fhttp.HeaderOrderKey] = append(h[fhttp.HeaderOrderKey], "content-encoding")
	}
	h[fhttp.HeaderOrderKey] = append(h[fhttp.HeaderOrderKey], "connection", "host", "accept-encoding", "content-length")
	return h
}
