# Claude Code 2.1.286 request fixtures

Captured on 2026-10-03 from the unmodified darwin-x64 native executable
(SHA-256 `53e6a936e89519d695230f9cc97943991286b72766674fba11bee845f0a7c047`).
The CLI used synthetic credentials and isolated configuration directories and
sent requests to a loopback mock. No Anthropic inference calls were made.

Scenarios: bare auto mode, a Read tool round trip, explicit default permission
mode, and a synthetic HTTP 503 followed by retry. Home paths, fixture paths and
OS user identity are placeholders. Account UUIDs are empty; device/session IDs
were generated only for these test configurations. Transport-specific headers
and the synthetic API key were removed; body structure and application headers
are retained from the normalized captures.

These fixtures establish client serialization and proxy-preservation contracts.
They do not establish upstream acceptance of private beta features, entitlement,
model availability, pricing, or equivalence on other platforms/client versions.
In particular, beta tokens are conditional capabilities, not a universal list to
inject into every account or request.
