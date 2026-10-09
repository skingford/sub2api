package httputil

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestOriginalRequestEncodingSurvivesPrereadAndOwnsBytes(t *testing.T) {
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	_, _ = w.Write([]byte(samplePayload))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := newRequestWithBody(t, compressed.Bytes(), "gzip")
	body, err := ReadRequestBodyWithPrealloc(req)
	if err != nil {
		t.Fatal(err)
	}
	req.Body = NewPrereadBody(body)
	_, _ = io.ReadAll(req.Body)
	again, err := ReadRequestBodyWithPrealloc(req)
	if err != nil {
		t.Fatal(err)
	}
	encoding, wire, ok := OriginalRequestEncoding(req.Clone(req.Context()), again)
	if !ok || encoding != "gzip" || !bytes.Equal(wire, compressed.Bytes()) {
		t.Fatal("encoded body lost across preread/clone")
	}
	wire[0] ^= 0xff
	_, restored, _ := OriginalRequestEncoding(req, body)
	if !bytes.Equal(restored, compressed.Bytes()) {
		t.Fatal("returned buffer modified stored evidence")
	}
	if _, _, ok := OriginalRequestEncoding(req, append(body, ' ')); ok {
		t.Fatal("changed logical bytes restored old wire body")
	}
	other := newRequestWithBody(t, body, "")
	if _, _, ok := OriginalRequestEncoding(other, body); ok {
		t.Fatal("encoded bytes leaked across requests")
	}
}

func TestOriginalRequestEncodingRejectsCorruptOrOversizedGzip(t *testing.T) {
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	// Streaming input avoids allocating a second full-size decompressed body.
	_, err := io.CopyN(w, zeroEncodingReader{}, maxDecompressedBodySize+1)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := newRequestWithBody(t, compressed.Bytes(), "gzip")
	_, err = ReadRequestBodyWithPrealloc(req)
	var tooLarge *http.MaxBytesError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected decoded size limit, got %v", err)
	}
	if _, _, ok := OriginalRequestEncoding(req, nil); ok {
		t.Fatal("truncated body retained for replay")
	}
	compressed.Reset()
	w = gzip.NewWriter(&compressed)
	_, _ = w.Write([]byte(samplePayload))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	broken := bytes.Clone(compressed.Bytes())
	broken[len(broken)-8] ^= 0xff // Corrupt the CRC, after valid JSON bytes.
	req = newRequestWithBody(t, broken, "gzip")
	if _, err := ReadRequestBodyWithPrealloc(req); err == nil {
		t.Fatal("corrupt CRC was accepted")
	}
	if _, _, ok := OriginalRequestEncoding(req, []byte(samplePayload)); ok {
		t.Fatal("unverified encoded bytes retained")
	}
}

type zeroEncodingReader struct{}

func (zeroEncodingReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
