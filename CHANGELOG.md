# Changelog

## 0.1.0 — 2026-07-03

First public release. MCP server that exposes the
[`agent-validate`](../agent-validate) library plus a small set of
discovery tools over the Model Context Protocol stdio transport.

**Tools:**

- `get_card` — fetch + parse + summarize an `agent.json`
- `validate_card` — schema validation + soft lint warnings (delegates
  to the embedded `agentvalidate.Validate` + `agentvalidate.Lint`)
- `list_capabilities` — return just the `capabilities` array
- `find_capability` — does this agent declare capability X?
- `resolve_wellknown` — turn a domain into the canonical
  `/.well-known/agent.json` URL

**Implementation notes:**

- Single binary, stdlib-only beyond `github.com/modelcontextprotocol/go-sdk`
  v1.6.1 and `github.com/NovaLux12/agent-validate` v0.1.1.
- Validation is delegated to the published `agentvalidate` library,
  not duplicated, so the server stays schema-accurate against the
  upstream `reflectt/agent-identity-kit` spec without separate maintenance.
- v1 schema has no `skills[]` (that's the A2A format); discovery
  primitive is the `capabilities` array. Documented in README.

**Tests:**

- 12 unit tests covering the tool handlers (in-process, no MCP
  transport plumbing) and the schema-lint roundtrip via the library.
- `scripts/smoke.sh` — end-to-end stdio smoke that spawns the binary,
  sends 6 JSON-RPC requests, and asserts the response shape.
- 13 tests total + 1 smoke script.

**Compatibility:** Targets the
[`reflectt/agent-identity-kit`](https://github.com/reflectt/agent-identity-kit)
v1 schema. Linux/darwin/windows × amd64/arm64 via Go cross-compilation.