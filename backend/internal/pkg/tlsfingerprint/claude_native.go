package tlsfingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ClaudeCode2292 returns the cold-connection profile measured from the official
// 2.1.292 Linux/macOS x64 binaries in the offline capture lab. It does not reuse captured
// randomness, session IDs or key material, and does not disable certificate checks.
func ClaudeCode2292() *Profile {
	return &Profile{
		Name: "Claude Code 2.1.292 x64", NativeHTTP1: true,
		CipherSuites: []uint16{4865, 4866, 4867, 49195, 49199, 49196, 49200, 52393, 52392, 49161, 49171, 49162, 49172, 156, 157, 47, 53},
		Curves:       []uint16{4588, 29, 23, 24}, PointFormats: []uint16{0},
		SignatureAlgorithms: []uint16{0x0403, 0x0804, 0x0401, 0x0503, 0x0805, 0x0501, 0x0806, 0x0601, 0x0201},
		ALPNProtocols:       []string{"http/1.1"}, SupportedVersions: []uint16{0x0304, 0x0303},
		KeyShareGroups: []uint16{4588, 29}, PSKModes: []uint16{1},
		Extensions: []uint16{0, 23, 65281, 10, 11, 35, 16, 5, 13, 18, 51, 45, 43},
	}
}

// ClaudeCode2295 uses the same measured cold handshake as 2.1.292, qualified
// separately for Linux x64. A distinct name keeps connection pools independent.
func ClaudeCode2295() *Profile {
	p := ClaudeCode2292()
	p.Name = "Claude Code 2.1.295 Linux x64"
	return p
}

// Clone freezes the profile used by a pooled transport. Later cache/configuration
// updates must not change handshakes on an already-created client.
func (p *Profile) Clone() *Profile {
	if p == nil {
		return nil
	}
	q := *p
	q.CipherSuites = append([]uint16(nil), p.CipherSuites...)
	q.Curves = append([]uint16(nil), p.Curves...)
	q.PointFormats = append([]uint16(nil), p.PointFormats...)
	q.SignatureAlgorithms = append([]uint16(nil), p.SignatureAlgorithms...)
	q.ALPNProtocols = append([]string(nil), p.ALPNProtocols...)
	q.SupportedVersions = append([]uint16(nil), p.SupportedVersions...)
	q.KeyShareGroups = append([]uint16(nil), p.KeyShareGroups...)
	q.PSKModes = append([]uint16(nil), p.PSKModes...)
	q.Extensions = append([]uint16(nil), p.Extensions...)
	return &q
}

// CacheKey prevents a changed TLS/HTTP profile from reusing a stale transport.
func (p *Profile) CacheKey() string {
	encoded, _ := json.Marshal(p)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
