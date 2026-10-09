package httputil

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net/http"
)

type originalRequestEncodingKey struct{}

type originalRequestEncoding struct {
	encoding string
	wire     []byte
	digest   [sha256.Size]byte
}

// OriginalRequestEncodingName reports only successfully verified ingress
// encoding, even after logical-body edits. It does not authorize replay;
// OriginalRequestEncoding must still check the final body's digest for that.
func OriginalRequestEncodingName(req *http.Request) string {
	if req == nil {
		return ""
	}
	value, _ := req.Context().Value(originalRequestEncodingKey{}).(originalRequestEncoding)
	return value.encoding
}

// Remember the successfully decoded wire body per request, not per client or
// account. Middleware may replace Body with a PrereadBody; context survives it.
func retainRequestEncoding(req *http.Request, encoding string, raw, decoded []byte) {
	if encoding != "gzip" {
		return
	}
	value := originalRequestEncoding{encoding: encoding, wire: raw, digest: sha256.Sum256(decoded)}
	*req = *req.WithContext(context.WithValue(req.Context(), originalRequestEncodingKey{}, value))
}

// OriginalRequestEncoding returns a private copy only if the caller's final
// logical body still exactly matches the fully decoded original. Routing or
// policy changes must never restore an earlier body or another session's bytes.
// The caller owns deciding whether its destination/protocol supports this form.
func OriginalRequestEncoding(req *http.Request, finalBody []byte) (string, []byte, bool) {
	if req == nil {
		return "", nil, false
	}
	value, ok := req.Context().Value(originalRequestEncodingKey{}).(originalRequestEncoding)
	if !ok || value.digest != sha256.Sum256(finalBody) {
		return "", nil, false
	}
	return value.encoding, bytes.Clone(value.wire), true
}
