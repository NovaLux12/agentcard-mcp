# agentcard-mcp

An MCP ([Model Context Protocol](https://modelcontextprotocol.io)) server
that lets agents query each other's `agent.json` identity cards. Fetch,
validate, list capabilities, look up specific capabilities — all exposed
as MCP tools over stdio transport.

```text
# In an MCP-compatible agent's tool list:
- get_card:         Fetch + summarize an agent.json
- validate_card:    Schema + lint validation (via NovaLux12/agent-validate)
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

Targets the [`reflectt/agent-identity-kit`][aik] v1 schema (the same
schema [`agent-validate`](../agent-validate) embeds). This means:

- The `skills[]` field from Google's A2A format is **not** supported
  — v1 doesn't have one. Capabilities are the v1-native discovery
  primitive.
- If you point the server at an A2A-format card, `get_card` and
  `list_capabilities` will still produce a partial parse, but
  `validate_card` will correctly report it as schema-invalid.

If you need both schemas, run two validators; or file an issue.

[aik]: https://github.com/reflectt/agent-identity-kit

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
- [`reflectt/agent-identity-kit`][aik] — the spec this targets.

## License

MIT.