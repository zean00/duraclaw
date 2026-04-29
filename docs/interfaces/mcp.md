# MCP Integration

Duraclaw can call MCP servers as durable tool providers. MCP calls persist intent and result summaries and receive Duraclaw execution context.

## Transports

Supported transports:

- HTTP
- SSE
- stdio JSON-RPC
- opt-in persistent stdio JSON-RPC

HTTP and SSE calls propagate context with headers:

```text
X-Customer-ID
X-User-ID
X-Agent-Instance-ID
X-Session-ID
X-Run-ID
X-Tool-Call-ID
X-Request-ID
```

Stdio calls inject context into the process environment and JSON-RPC metadata envelope where supported.

## Global Configuration

```bash
DURACLAW_MCP_CONFIG='{
  "servers": [
    {
      "name": "tools",
      "transport": "http",
      "base_url": "http://mcp.internal",
      "max_concurrent": 4,
      "max_retries": 0
    }
  ]
}'
```

Agent instance versions can add per-version servers through `mcp_config.servers`.

## Admin Discovery

- `GET /admin/mcp/servers`
- `GET /admin/mcp/servers/{server_name}/tools`
- `GET /admin/mcp/servers/{server_name}/resources`
- `GET /admin/mcp/servers/{server_name}/resources/read?uri={uri}`
- `GET /admin/mcp/servers/{server_name}/prompts`
- `POST /admin/mcp/servers/{server_name}/prompts/{prompt_name}/get`
- `POST /admin/mcp/notifications`

## Retry Policy

HTTP MCP retries are opt-in. Keep retries disabled for side-effecting tools unless the target server supports idempotency.
