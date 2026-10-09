# Claude Code 2.1.295 synthetic evidence

Pinned Linux x64 binary SHA-256: `4503bfe11a6c7fcc1e0b39b5e0d347c04248f750b03b0977b3ad6b531fe6f358`.
Sources and original capture hashes are in `provenance.json`. Model requests used the unmodified CLI,
Docker network-none, dummy credentials and a local TLS responder; their wire bytes were independently
checked against PCAP with zero kernel drops. Large gzip fixtures retain original compressed bytes and
a decoded SHA-256 instead of duplicating the large plaintext.

`compact-first.json` is an actual rejected continuation with unmodified production recovery.
`compact-retained.json` is an actual continuation from the same unmodified CLI after a local experiment
allowed only 2.1.295 compaction inference; it exposes the second, independently rejected wrapper.
Neither experiment asserts official acceptance, subscription eligibility, signatures or billing.

See `docs/claude-source-runtime-audit-20261009.md` (CC-20261009-011) and
`docs/claude-295-compatibility-fix-20261009.md` (CC-20261009-012).
