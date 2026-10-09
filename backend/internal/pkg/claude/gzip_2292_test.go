package claude

import (
	"context"
	"testing"
)

func TestGzipHeaderOrder2292Scope(t *testing.T) {
	ctx := context.Background()
	if GzipUsesApplicationHeader2292(ctx) {
		t.Fatal("no request marker")
	}
	// Native vectors live in repository/service capture regressions. These are
	// malformed or incomplete envelopes, not synthesized native oracle vectors.
	for _, wire := range [][]byte{nil, {0x1f, 0x8b, 8, 0, 0, 0, 0, 0, 0, 0xff}, make([]byte, 24)} {
		if GzipUsesApplicationHeader2292(WithGzipHeaderOrder2292(ctx, wire)) {
			t.Fatal("incomplete framing classified as blocks")
		}
	}
}
