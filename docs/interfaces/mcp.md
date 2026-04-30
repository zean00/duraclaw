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

MCP HTTP and SSE calls also receive Nexus channel headers when present:

```text
X-Channel-Type
X-Channel-User-ID
X-Channel-Conversation-ID
```

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

## Tool Access Rules

MCP tool access can be narrowed per customer, agent instance, user, and server. If no rule exists, Duraclaw preserves the configured server behavior and exposes all discovered tools. A customer-level rule is the baseline; a user-level rule for the same customer, agent instance, and server replaces that baseline.

Rules support both `allowed_tools` and `denied_tools`. `denied_tools` wins over `allowed_tools`. When `allowed_tools` is empty, every discovered tool is allowed except denied tools.

Access is enforced when building the model-visible MCP tool manifest and again immediately before MCP tool execution.

## Admin Discovery

- `GET /admin/mcp/servers`
- `GET /admin/mcp/servers/{server_name}/tools`
- `GET /admin/mcp/servers/{server_name}/resources`
- `GET /admin/mcp/servers/{server_name}/resources/read?uri={uri}`
- `GET /admin/mcp/servers/{server_name}/prompts`
- `POST /admin/mcp/servers/{server_name}/prompts/{prompt_name}/get`
- `POST /admin/mcp/notifications`

Customer-level access rules:

- `PUT /admin/mcp/tool-access/customers/{customer_id}/agent-instances/{agent_instance_id}/servers/{server_name}`
- `GET /admin/mcp/tool-access/customers/{customer_id}/agent-instances/{agent_instance_id}/servers/{server_name}`
- `DELETE /admin/mcp/tool-access/customers/{customer_id}/agent-instances/{agent_instance_id}/servers/{server_name}`

User-level access rules:

- `PUT /admin/mcp/tool-access/customers/{customer_id}/agent-instances/{agent_instance_id}/users/{user_id}/servers/{server_name}`
- `GET /admin/mcp/tool-access/customers/{customer_id}/agent-instances/{agent_instance_id}/users/{user_id}/servers/{server_name}`
- `DELETE /admin/mcp/tool-access/customers/{customer_id}/agent-instances/{agent_instance_id}/users/{user_id}/servers/{server_name}`

## Retry Policy

HTTP MCP retries are opt-in. Keep retries disabled for side-effecting tools unless the target server supports idempotency.
