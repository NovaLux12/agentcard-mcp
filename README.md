# agentcard-mcp

An MCP ([Model Context Protocol](https://modelcontextprotocol.io)) server
that lets agents query each other's `agent.json` identity cards. Fetch,
validate, list capabilities, look up specific capabilities — all exposed
as MCP tools over stdio transport.

```text
# In an MCP-compatible agent's tool list:
- get_card:         Fetch + summarize an agent.json
- validate_card:    Schema + lint validation (via NovaLux12/agent-validate)
- lint_card:        Structured lint warnings (code, path, message separately)
- list_capabilities: Just the capabilities array
- find_capability:  Does this agent declare capability X?
- resolve_wellknown: domain → /.well-known/agent.json
```

## Why this exists

[`NovaLux12/agent-validate`](../agent-validate) is the validator. This
is its companion: an MCP server that lets agents actually *use* that
validation as part of their reasoning. Useful scenarios:

- "Before I delegate this task to agent X, check whether it has the
  `code-review` capability." → `find_capability`.
- "Is this card I'm about to publish actually valid against the
  current schema?" → `validate_card` against a local path.
- "What's at this domain's /.well-known/agent.json?" → `resolve_wellknown`
  then `get_card`.

## Install

**Homebrew** (planned):

```sh
brew install NovaLux12/tap/agentcard-mcp
```

**`go install`:**

```sh
go install github.com/NovaLux12/agentcard-mcp@latest
```

**Direct download** — grab the binary for your OS from
[Releases](https://github.com/NovaLux12/agentcard-mcp/releases):

```sh
# Linux x86_64 example
curl -L https://github.com/NovaLux12/agentcard-mcp/releases/latest/download/agentcard-mcp_linux_amd64.tar.gz \
  | tar xz -C /usr/local/bin agentcard-mcp
```

**Build from source:**

```sh
git clone https://github.com/NovaLux12/agentcard-mcp.git
cd agentcard-mcp
go build -ldflags="-s -w" -o agentcard-mcp .
```

## Wire it up to an MCP client

The server speaks MCP over stdio. Configure your MCP client to spawn
the binary:

```json
// Claude Desktop / OpenClaw / similar
{
  "mcpServers": {
    "agentcard": {
      "command": "/usr/local/bin/agentcard-mcp"
    }
  }
}
```

Or for development, run it directly to see the JSON-RPC traffic:

```sh
agentcard-mcp
# Server waits for JSON-RPC on stdin, writes responses to stdout.
```

## Tools

### `get_card`

**Arguments:** `source` (URL or local path)

**Returns:** parsed summary including name, handle, description, owner,
capabilities, protocols, and whether `endpoints.card` is declared.

### `validate_card`

**Arguments:** `source` (URL or local path)

**Returns:** schema validation result (PASS/FAIL with errors) plus soft
lint warnings. Uses
[`agentvalidate`](https://github.com/NovaLux12/agent-validate) directly
as a library, so the validator and the server never drift apart.

### `lint_card`

**Arguments:** `source` (URL or local path)

**Returns:** structured lint warnings with the code, path, and message
broken out into separate fields, plus a count-by-code summary and a
deduplicated list of distinct codes. Schema validation is **not** run
— use `validate_card` if you need that. Use `lint_card` when you want
to filter by warning code (`count_by_code["H003"]`), aggregate across
many cards, or render warnings in a UI without re-parsing the
human-readable form.

```json
{
  "source": "agent.json",
  "warnings": [
    {"code": "H003", "path": "agent.handle", "message": "handle domain is a personal email provider..."}
  ],
  "codes": ["H003"],
  "count_by_code": {"H003": 1},
  "has_warnings": true
}
```

### `list_capabilities`

**Arguments:** `source`

**Returns:** just the `capabilities` array.

### `find_capability`

**Arguments:** `source`, `capability` (the tag to look up)

**Returns:** whether the capability is declared + the full list of
declared tags.

### `resolve_wellknown`

**Arguments:** `domain` (base URL or bare domain)

**Returns:** the canonical `/.well-known/agent.json` URL. Useful before
calling `get_card` etc. when you know the domain but not the path.

## Compatibility

Targets the [`NovaLux12/agent-identity-kit`][aik] spec (the same schema
[`agent-validate`](../agent-validate) embeds). This means:

- **Known limitation:** `agent-validate` v0.2.0 embeds the v1.0
  schema only. Cards that use v1.1+ fields (`agent.kind`, `scope`,
  `vouched_by`, `offers`/`seeks`, `owner: null`) are valid per the
  current spec (v1.3) but `validate_card` will report them as schema
  errors until agent-validate ships a newer schema. The parse-only
  tools (`get_card`, `list_capabilities`, `find_capability`,
  `lint_card`) handle v1.1+ cards, including the spec-sanctioned
  `owner: null` form for autonomous agents.
- The `skills[]` field from Google's A2A format is **not** supported
  — the agent.json spec doesn't have one. Capabilities are the
  native discovery primitive.
- If you point the server at an A2A-format card, `get_card` and
  `list_capabilities` will still produce a partial parse, but
  `validate_card` will correctly report it as schema-invalid.

If you need both schemas, run two validators; or file an issue.

[aik]: https://github.com/NovaLux12/agent-identity-kit

## Use as a library

The tool handlers are exported from `main.go` (each starts with
`register…Tool`). If you want to embed the same behaviour in a
different MCP server, copy the patterns — but note that `main.go`
is intentionally single-file so the server stays readable as one
unit.

## Tests

`go test -race -count=1 ./...` — covers the tool handlers in-process
(no MCP transport plumbing) plus a live stdio smoke test via the
helper script under `scripts/`.

## Related

- [`NovaLux12/agent-validate`](../agent-validate) — the schema
  validation library this server uses.
- [`NovaLux12/agent-card`](../agent-card) — example published
  agent card.
- [`NovaLux12/agent-identity-kit`][aik] — the spec this targets.

## License

MIT.