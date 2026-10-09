package claude

import (
	"bytes"
	"context"
)

type gzipApplicationHeader2292Key struct{}

// WithGzipHeaderOrder2292 records the encoding-header position for an unchanged,
// fully decoded/CRC-verified native 2.1.292 gzip request. Callers must establish
// that scope first; the framing is not an authentication or version signal.
//
// The pinned CLI's oos/Sto (chunk-47d8fnm7.js) construction concatenates
// Z_SYNC_FLUSH pieces, then an empty final deflate block (03 00), CRC and size.
// Runtime gzip uses Z_FINISH instead. Both can have OS=255, so that byte alone
// cannot distinguish them. Only the complete fixed header AND block terminator
// select the application-sorted header position. No gzip bytes are rewritten.
func WithGzipHeaderOrder2292(ctx context.Context, wire []byte) context.Context {
	blocks := len(wire) >= 24 &&
		bytes.Equal(wire[:10], []byte{0x1f, 0x8b, 8, 0, 0, 0, 0, 0, 0, 0xff}) &&
		bytes.Equal(wire[len(wire)-14:len(wire)-8], []byte{0, 0, 0xff, 0xff, 3, 0})
	return context.WithValue(ctx, gzipApplicationHeader2292Key{}, blocks)
}

// GzipUsesApplicationHeader2292 is request-local and survives clones/retries.
func GzipUsesApplicationHeader2292(ctx context.Context) bool {
	return ctx != nil && ctx.Value(gzipApplicationHeader2292Key{}) == true
}
