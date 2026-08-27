# Changelog

All notable changes to this project are documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## 0.2.1 — 2026-08-27

**Fixed:**

- **`get_card` / `list_capabilities` / `find_capability` no longer error
  on cards with `owner: null`.** The spec (agent-identity-kit §3.3, v1.1+)
  sanctions a null `owner` for autonomous agents. Previously, a single
  `tools/call` against a valid autonomous-agent card returned
  `card.owner: expected object, got <nil>` and the card could not be
  summarised at all. Null `owner` is now skipped (owner fields empty in
  the summary), matching the schema's `type: ["object", "null"]`.
  Regression test added (`TestSummarizeNullOwner`).

**Changed:**

- Dependency bumps: `agent-validate` v0.1.1 → v0.2.0 (adds the
  `agentvalidate.Report` public API and `--json` output mode),
  `modelcontextprotocol/go-sdk` v1.6.1 → v1.7.0.
- README Compatibility section now documents that agent-validate v0.2.0
  embeds the v1.0 schema only, so v1.1+ fields (`agent.kind`, `scope`,
  `vouched_by`, `offers`/`seeks`, `owner: null`) are reported as schema
  errors by `validate_card` until agent-validate ships a newer schema.
- Header comment references the canonical `NovaLux12/agent-identity-kit`
  spec instead of the silent upstream.

## 0.2.0 — 2026-07-03

Add structured lint tool.

**Added:**

- **`lint_card` tool.** Returns soft lint warnings as structured fields
  (code, path, message separately) plus a deduplicated `codes` list and
  a `count_by_code` map. Schema validation is **not** run here — use
  `validate_card` for that. Useful when callers want to filter by code,
  aggregate across many cards, or render warnings in a UI without
  re-parsing the human-readable form.

**Tests:** added 5 lint_card tests (clean card, multi-code aggregation,
renderLint clean case, renderLint with warnings, renderLint without
path). All pass.

## 0.1.1 — 2026-07-03

Post-verifier fixes from the M3 review pass.

**Fixed (CRITICAL):**

- **loadSource now explicitly rejects stdin-equivalent paths.** The
  previous version claimed "stdin NOT supported" but only filtered
  empty input and embedded NUL bytes, falling through to `os.ReadFile`
  for `-`, `/dev/stdin`, `/dev/zero`, `/proc/self/fd/0`, etc. A
  single `tools/call` with `arguments.source="/dev/stdin"` would
  race the MCP SDK's JSON-RPC reader and corrupt the wire protocol
  (or block indefinitely waiting for EOF). loadSource now:
  - Rejects `-` explicitly
  - Rejects `/dev/...` and `/proc/...` prefixes
  - Uses `url.Parse` for scheme detection (case-insensitive, RFC 3986)
  - Rejects any non-http(s) scheme (`file://`, `ftp://`, `gopher://`)

**Fixed (MEDIUM):**

- **summarize now distinguishes wrong-type fields from absent ones.**
  Previously, a `capabilities: "web-search"` (string instead of array)
  silently produced `Capabilities: []`. Now it returns an error like
  `card.capabilities: expected array, got string`. This matters because
  `find_capability` and `list_capabilities` call summarize directly
  without running the schema validator, so silent zero values would
  produce misleading "not found" answers.
- **Protocols sorted deterministically.** Map iteration in Go is
  randomised; the previous Protocols output varied across calls.
  Added `sort.Strings(out.Protocols)` so log diffs and snapshots are
  stable.

**Fixed (LOW):**

- **Defensive copy of Capabilities in find_capability.** The output
  field used to alias `summary.Capabilities`. Today that's a fresh
  slice, but the aliasing is a footgun for future refactors.

**Tests:** 13 → 20, with new cases for stdin-equivalent rejection,
uppercase scheme acceptance, wrong-type-field errors, and protocol
sort order.

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