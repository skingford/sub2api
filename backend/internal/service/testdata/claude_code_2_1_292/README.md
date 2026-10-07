# Native Claude Code 2.1.292 captures

These fixtures were emitted by unmodified official native binaries with
synthetic API keys, an empty tool list, custom test prompts, `--bare`, and
explicit default permission mode. They are not production OAuth captures.

- `linux-firstparty`: Linux x64, in a Docker `--network none` container.
  The canonical API hostname resolved to loopback inside that container.
  The local server received 24 headers and a 1,083-byte JSON body. The full
  HTTP request was independently recovered from the PCAP using the mock
  server's TLS session keys and checked against the received body SHA-256.
- `macos-loopback`: macOS x64, with a process sandbox allowing only the local
  TLS endpoint. The body was 928 bytes. No privileged packet capture was used.

Both binaries embed Bun 1.4.3 (`eecfd55de`). Official manifest checksums and
body hashes are in `provenance.json`.

The `.body.json` files retain the exact received body bytes. The formatted
`.request.json` files remove transport-owned headers (`Host`, `Connection`,
`Content-Length`, `Accept-Encoding`) and the synthetic API-key header. Tests
replace authentication explicitly, check credential isolation, and compare
the forwarded body byte for byte. Native account and session identifiers are
test-generated; account UUIDs are empty. Attribution fields are opaque fixture
data and must not be reused or manufactured for other requests.

The capture verifies client serialization. It does not establish upstream
acceptance, entitlement, billing classification, TLS fingerprint equivalence,
or identical behavior for other modes and model capabilities.

Compared with the same 2.1.291 scenarios, no normalized business-field or beta differences were observed. Version attribution and generated identifiers differ. Opaque attribution values were recorded, not reverse-engineered or recreated.
