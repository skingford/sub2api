//go:build unit

package service

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnthropicSSEReaderFraming(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		t.Run(strings.ReplaceAll(strings.ReplaceAll(ending, "\r", "CR"), "\n", "LF"), func(t *testing.T) {
			input := strings.Join([]string{": keepalive", "", "event: old", "data:  first ", "id: ignored", "event: new", "data", "data:\tlast", "", "data: next", "", ""}, ending)
			r := newAnthropicSSEReader(&claudeSSEByteReader{strings.NewReader(input)}, 1024)
			require.True(t, r.Scan())
			require.Equal(t, "new", r.event)
			require.Equal(t, " first \n\n\tlast", r.data)
			require.True(t, r.Scan())
			require.Empty(t, r.event)
			require.Equal(t, "next", r.data)
			require.False(t, r.Scan())
			require.NoError(t, r.Err())
		})
	}
}

func TestAnthropicSSEReaderBoundsAndReadFailure(t *testing.T) {
	for _, input := range []string{
		"data: " + strings.Repeat("x", 70),
		strings.Repeat("data: x\n", 9),
		strings.Repeat(": ignored\n", 7),
	} {
		r := newAnthropicSSEReader(strings.NewReader(input), 64)
		require.False(t, r.Scan())
		require.Error(t, r.Err())
		require.False(t, r.Scan())
	}
	t.Run("clean EOF retains compatibility", func(t *testing.T) {
		r := newAnthropicSSEReader(strings.NewReader("data: final"), 64)
		require.True(t, r.Scan())
		require.Equal(t, "final", r.data)
		require.False(t, r.Scan())
		require.NoError(t, r.Err())
	})
	t.Run("read failure never flushes pending frame", func(t *testing.T) {
		r := newAnthropicSSEReader(io.MultiReader(strings.NewReader("data: complete\n\ndata: partial\n"), claudeSSEFailReader{}), 64)
		require.True(t, r.Scan())
		require.Equal(t, "complete", r.data)
		require.False(t, r.Scan())
		require.ErrorIs(t, r.Err(), io.ErrUnexpectedEOF)
		require.Empty(t, r.data)
	})
	t.Run("completed events have independent bounds", func(t *testing.T) {
		r := newAnthropicSSEReader(strings.NewReader(strings.Repeat("data: "+strings.Repeat("x", 55)+"\n\n", 3)), 64)
		for range 3 {
			require.True(t, r.Scan())
		}
		require.False(t, r.Scan())
		require.NoError(t, r.Err())
	})
}

type claudeSSEFailReader struct{}

func (claudeSSEFailReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
