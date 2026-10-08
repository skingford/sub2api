package repository

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestNativeClaudeHTTP1WireOrderAndReuse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	captured := make(chan []string, 2)
	serverDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		reader := bufio.NewReader(conn)
		for range 2 {
			var lines []string
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					serverDone <- err
					return
				}
				if line == "\r\n" {
					break
				}
				lines = append(lines, strings.TrimRight(line, "\r\n"))
			}
			// The native serializer must not change the JSON body.
			body := make([]byte, len(`{"message":"hello"}`))
			if _, err := io.ReadFull(reader, body); err != nil {
				serverDone <- err
				return
			}
			if string(body) != `{"message":"hello"}` {
				serverDone <- fmt.Errorf("body changed: %s", body)
				return
			}
			captured <- lines
			if _, err := io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nOK"); err != nil {
				serverDone <- err
				return
			}
		}
		serverDone <- nil
	}()
	transport := newNativeClaudeHTTP1Transport(&http.Transport{MaxIdleConns: 2, MaxIdleConnsPerHost: 2, IdleConnTimeout: time.Second})
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	for range 2 {
		req, err := http.NewRequest(http.MethodPost, "http://"+listener.Addr().String()+"/v1/messages", bytes.NewBufferString(`{"message":"hello"}`))
		require.NoError(t, err)
		req.Header = http.Header{"x-app": {"cli"}, "content-type": {"application/json"}, "User-Agent": {"claude-cli/2.1.292 (external, sdk-cli)"}, "Accept": {"application/json"}, "authorization": {"Bearer synthetic"}}
		original := req.Header.Clone()
		resp, err := client.Do(req)
		require.NoError(t, err)
		payload, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, "OK", string(payload))
		require.Equal(t, original, req.Header, "serializer must own its header map")
		lines := <-captured
		var names []string
		for _, line := range lines[1:] {
			names = append(names, strings.SplitN(line, ":", 2)[0])
		}
		require.Equal(t, []string{"Accept", "Authorization", "Content-Type", "User-Agent", "x-app", "Connection", "Host", "Accept-Encoding", "Content-Length"}, names)
		require.Equal(t, "POST /v1/messages HTTP/1.1", lines[0])
		require.Contains(t, lines, "Authorization: Bearer synthetic")
		require.Contains(t, lines, "Connection: keep-alive")
	}
	require.NoError(t, <-serverDone)
}

func TestNativeClaudeHTTP1Cancellation(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() }))
	defer server.Close()
	transport := newNativeClaudeHTTP1Transport(&http.Transport{})
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	result := make(chan error, 1)
	go func() { _, err := (&http.Client{Transport: transport}).Do(req); result <- err }()
	<-entered
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("request cancellation was lost")
	}
}

func TestTLSClientPoolSeparatesProfileContents(t *testing.T) {
	svc, ok := NewHTTPUpstream(nil).(*httpUpstreamService)
	require.True(t, ok)
	profile := tlsfingerprint.ClaudeCode2292()
	first, err := svc.getClientEntryWithTLS("", 292, 1, profile, service.HTTPUpstreamProfileDefault, false, false)
	require.NoError(t, err)
	again, err := svc.getClientEntryWithTLS("", 292, 1, profile.Clone(), service.HTTPUpstreamProfileDefault, false, false)
	require.NoError(t, err)
	require.Same(t, first, again)
	profile.Curves = []uint16{29, 23, 24}
	changed, err := svc.getClientEntryWithTLS("", 292, 1, profile, service.HTTPUpstreamProfileDefault, false, false)
	require.NoError(t, err)
	require.NotSame(t, first, changed)
	require.Len(t, svc.clients, 2)
	first.client.CloseIdleConnections()
	changed.client.CloseIdleConnections()
}

func TestNativeClaudeHTTPSProxyRetainsTLSValidation(t *testing.T) {
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("CONNECT must not be sent before the proxy certificate is verified")
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	require.NoError(t, err)
	transport, err := buildUpstreamTransportWithTLSFingerprint(defaultPoolSettings(nil), proxyURL, tlsfingerprint.ClaudeCode2292())
	require.NoError(t, err)
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	_, err = transport.DialTLSContext(ctx, "tcp", "api.anthropic.com:443")
	require.Error(t, err)
	require.Contains(t, err.Error(), "TLS handshake with HTTPS proxy")
	require.Contains(t, err.Error(), "certificate")
}

func TestNativeClaudeProxyHandshakeCancellation(t *testing.T) {
	accepted := make(chan struct{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		close(accepted)
		_, _ = io.Copy(io.Discard, conn)
	}()
	proxyURL, err := url.Parse("http://" + listener.Addr().String())
	require.NoError(t, err)
	dialer := tlsfingerprint.NewHTTPProxyDialer(tlsfingerprint.ClaudeCode2292(), proxyURL)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := dialer.DialTLSContext(ctx, "tcp", "api.anthropic.com:443"); result <- err }()
	<-accepted
	cancel()
	select {
	case err := <-result:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("cancelled proxy handshake remained blocked")
	}
	<-done
}
