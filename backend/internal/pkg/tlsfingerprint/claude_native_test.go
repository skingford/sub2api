package tlsfingerprint

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This digest comes from the official 2.1.292 Linux and macOS x64 captures,
// not from serializing the profile under test. Only per-connection randomness
// (random, session ID and ephemeral public keys) is excluded.
func TestClaudeNativeClientHelloGolden(t *testing.T) {
	for _, profile := range []*Profile{ClaudeCode2292(), ClaudeCode2295()} {
		t.Run(profile.Name, func(t *testing.T) { checkClaudeNativeClientHello(t, profile) })
	}
}

func checkClaudeNativeClientHello(t *testing.T, profile *Profile) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()
	tcpListener, ok := listener.(*net.TCPListener)
	require.True(t, ok)
	require.NoError(t, tcpListener.SetDeadline(time.Now().Add(5*time.Second)))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	dialer := NewDialer(profile, func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, listener.Addr().String())
	})
	result := make(chan error, 1)
	go func() {
		conn, err := dialer.DialTLSContext(ctx, "tcp", "api.anthropic.com:443")
		if conn != nil {
			_ = conn.Close()
		}
		result <- err
	}()
	conn, err := listener.Accept()
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
	header := make([]byte, 5)
	_, err = io.ReadFull(conn, header)
	require.NoError(t, err)
	require.Equal(t, []byte{22, 3, 1}, header[:3])
	n := int(binary.BigEndian.Uint16(header[3:]))
	require.Equal(t, 1499, n)
	hello := make([]byte, n)
	_, err = io.ReadFull(conn, hello)
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.Error(t, <-result, "receiving a ClientHello must not bypass server certificate verification")
	require.Equal(t, byte(1), hello[0])
	clear(hello[6:38])
	sessionLen := int(hello[38])
	clear(hello[39 : 39+sessionLen])
	pos := 39 + sessionLen
	pos += 2 + int(binary.BigEndian.Uint16(hello[pos:pos+2]))
	pos += 1 + int(hello[pos])
	extensionsLen := int(binary.BigEndian.Uint16(hello[pos : pos+2]))
	pos += 2
	end := pos + extensionsLen
	require.Equal(t, len(hello), end)
	for pos < end {
		kind := binary.BigEndian.Uint16(hello[pos : pos+2])
		size := int(binary.BigEndian.Uint16(hello[pos+2 : pos+4]))
		pos += 4
		if kind == 51 {
			for key := pos + 2; key < pos+size; {
				size := int(binary.BigEndian.Uint16(hello[key+2 : key+4]))
				key += 4
				clear(hello[key : key+size])
				key += size
			}
		}
		pos += size
	}
	sum := sha256.Sum256(hello)
	require.Equal(t, "8845ac2401a951ffc4acef2824c3422124c7883e0c9bc4b5f90d3c6be05da2f9", hex.EncodeToString(sum[:]))
}

func TestClaudeNativeProfileSnapshot(t *testing.T) {
	require.NotEqual(t, ClaudeCode2292().CacheKey(), ClaudeCode2295().CacheKey())
	original := ClaudeCode2292()
	snapshot := original.Clone()
	key := snapshot.CacheKey()
	original.Curves[0] = 29
	original.Extensions[0] = 23
	original.ALPNProtocols[0] = "h2"
	require.Equal(t, key, snapshot.CacheKey())
	require.NotEqual(t, key, original.CacheKey())
	require.Equal(t, uint16(4588), snapshot.Curves[0])
	require.Equal(t, "http/1.1", snapshot.ALPNProtocols[0])
}
