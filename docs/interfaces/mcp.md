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

## Model-Loop Tools

When MCP servers are configured, Duraclaw discovers allowed MCP tools and exposes them directly to the model loop as function tools. This is separate from workflow MCP nodes: the model can call an MCP tool during normal agent reasoning when the tool is visible and policy allows tool use.

Exposed MCP function names use this shape:

```text
mcp__{server_name}__{tool_name}__{stable_hash}
```

Server and tool names are normalized to provider-safe lowercase identifiers. The stable hash keeps names unique when different MCP servers or tools normalize to the same text. Tool descriptions include the original `{server}.{tool}` name, and MCP input schemas are passed through as function parameters.

Duraclaw rechecks MCP access immediately before execution, so a tool hidden by a later whitelist or blacklist change cannot be executed just because it appeared in a prior model turn.

MCP function calls count against the same `tool_config.max_tool_calls_per_run` limit as built-in model-loop tools. The limiter counts persisted local tool calls and persisted MCP calls for the run, so repeated MCP calls across multiple loop iterations cannot bypass the cap.

Policy and profile instructions can recommend when to use MCP tools, for example “use the catalog search tool before recommending products.” These instructions are advisory only. They cannot grant access to a blocked MCP tool, and whitelist/blacklist rules still win.

## Tool Access Rules

MCP tool access can be narrowed per customer, agent instance, user, and server. If no rule exists, Duraclaw preserves the configured server behavior and exposes all discovered tools. A customer-level rule is the baseline; a user-level rule for the same customer, agent instance, and server replaces that baseline.

Rules support both `allowed_tools` and `denied_tools`. `denied_tools` wins over `allowed_tools`. When `allowed_tools` is empty, every discovered tool is allowed except denied tools.

Access is enforced when building model-visible MCP function tools and again immediately before MCP tool execution.

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
