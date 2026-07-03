#!/usr/bin/env bash
# scripts/smoke.sh — end-to-end smoke test for the MCP server.
#
# Builds the binary, starts it on stdio, sends an initialize + a few
# tools/list + tools/call requests, and prints a digest of the
# responses. Exits non-zero on any error.

set -euo pipefail

# Resolve repo root regardless of where this script is invoked from.
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
cd "$ROOT"

echo "building ..."
go build -ldflags="-s -w" -o /tmp/agentcard-mcp-smoke .

# Write a fixture card the smoke will read.
cat > /tmp/agentcard-mcp-smoke-card.json <<'CARD'
{
  "version": "1.0",
  "agent": {
    "name": "TestAgent",
    "handle": "@test@example.com",
    "description": "Smoke-test card."
  },
  "owner": {"name": "Test Owner"},
  "capabilities": ["code-generation", "web-search"],
  "protocols": {"mcp": true, "agent-card": "1.0"},
  "endpoints": {"card": "https://example.com/.well-known/agent.json"},
  "updated_at": "2026-07-01T00:00:00Z"
}
CARD

# Send newline-delimited JSON over stdio.
{
    printf '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}\n'
    printf '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}\n'
    printf '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}\n'
    printf '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_card","arguments":{"source":"/tmp/agentcard-mcp-smoke-card.json"}}}\n'
    printf '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"validate_card","arguments":{"source":"/tmp/agentcard-mcp-smoke-card.json"}}}\n'
    printf '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"find_capability","arguments":{"source":"/tmp/agentcard-mcp-smoke-card.json","capability":"code-generation"}}}\n'
    printf '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"resolve_wellknown","arguments":{"domain":"example.com"}}}\n'
    sleep 1
} | /tmp/agentcard-mcp-smoke 2>/tmp/agentcard-mcp-smoke.err | python3 -c "
import sys, json
seen = set()
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        msg = json.loads(line)
    except Exception as e:
        print('PARSE ERR:', e, '|', line[:120])
        sys.exit(2)
    rid = msg.get('id')
    if 'result' in msg and 'tools' in msg['result']:
        names = [t['name'] for t in msg['result']['tools']]
        print('tools/list:', names)
        for want in ['get_card', 'validate_card', 'list_capabilities', 'find_capability', 'resolve_wellknown']:
            if want not in names:
                print('FAIL: missing tool', want)
                sys.exit(2)
    elif 'result' in msg and 'content' in msg['result']:
        for c in msg['result']['content']:
            if c.get('type') == 'text':
                print(f'id={rid}: {c[\"text\"][:200]}')
    elif 'result' in msg and 'serverInfo' in msg['result']:
        print('initialize OK:', msg['result']['serverInfo'])
    elif 'error' in msg:
        print('FAIL id=', rid, ':', msg['error'])
        sys.exit(2)
    seen.add(rid)
expected = {1, 2, 3, 4, 5, 6}
missing = expected - seen
if missing:
    print('FAIL: no responses for ids', missing)
    sys.exit(2)
print('OK: all 6 requests got responses')
"
status=$?
if [ -s /tmp/agentcard-mcp-smoke.err ]; then
    echo "--- server stderr ---"
    cat /tmp/agentcard-mcp-smoke.err
fi
exit $status