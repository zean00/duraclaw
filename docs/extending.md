# Extending Duraclaw

Duraclaw is designed around small durable contracts. Extensions should persist intent before side effects and persist enough result metadata to resume or audit later.

## Add a Provider

Implement the provider interfaces in `internal/providers`:

- `LLMProvider` for chat.
- `DurableProvider` when the provider can receive call metadata.
- `StreamingProvider` for model deltas.
- Embedding, transcription, upload, or media generation interfaces when needed.

Register the provider in `cmd/duraclaw/main.go` and expose any required environment variables in `cmd/duraclaw/config.go`.

Provider implementations should:

- Support model references consistently.
- Return usage when available.
- Preserve tool calls.
- Avoid logging full prompts or sensitive content by default.

## Add a Local Tool

Local tools live under `internal/tools`.

A tool should define:

- Name and description.
- JSON-schema-like parameters.
- Validation.
- Retryability.
- Execution with Duraclaw context.

Write-like tools should be non-retryable unless they have an idempotency key or are otherwise safe to repeat.

Built-in tool exposure is controlled in two layers. Agent instance version `tool_config` can set `allowed_tools` and `disabled_tools` for versioned behavior. Admin tool access rules can then narrow tools per customer/agent instance or per user through `/admin/tool-access/...`; user rules replace the customer/agent baseline, and denied tools win over allowed tools.

Some model providers only accept function names matching `^[a-zA-Z0-9_-]{1,128}$`. Use `tool_config.tool_aliases` to expose provider-safe names while Duraclaw still authorizes and executes the original tool names:

```json
{
  "tool_aliases": {
    "duraclaw.ask_user": "duraclaw_ask_user",
    "duraclaw.run_workflow": "duraclaw_run_workflow"
  }
}
```

Aliases are applied only to tools that are actually exposed for the current run after `allowed_tools`, `disabled_tools`, admin tool-access rules, and MCP access rules are evaluated. If an alias points at a hidden tool, Duraclaw does not reverse-map provider calls for that alias. If an applied alias conflicts with another exposed provider tool name, run setup fails instead of routing the call ambiguously.

When `profile_config.tool_selection.enabled` is true, Duraclaw shortlists the already-authorized model-loop tools before the main model call. Built-ins have default metadata, and custom tools can add optional selection hints through `tool_config.tool_metadata`:

```json
{
  "tool_metadata": {
    "customer_notes.capture": {
      "tags": ["note", "todo", "capture"],
      "side_effect": "write",
      "conflicts_with": ["remember", "create_reminder"]
    }
  }
}
```

These hints affect ranking only. They cannot expose tools hidden by agent version config, admin access rules, MCP access rules, prompt-injection blocking, or policy enforcement.

## Add an MCP Server

Prefer MCP for external tool surfaces. Configure globally with `DURACLAW_MCP_CONFIG` or per agent instance version with `mcp_config`.

For side-effecting MCP tools:

- Keep retries disabled unless idempotent.
- Use Duraclaw context headers for tenant/user/session/run scoping.
- Return concise result summaries.

## Add an Artifact Processor

Artifact processors implement modality-specific representation creation:

- Transcription.
- OCR.
- Vision summary.
- Document extraction.
- Video summary.
- Structured field extraction.

Processor behavior should:

- Persist processor intent before accessing media.
- Enforce artifact policy.
- Avoid raw binary payloads in logs.
- Return bounded summaries and representation metadata.
- Degrade oversized outputs when configured.

## Add a Workflow Node

Workflow nodes are durable graph steps. A node implementation should:

- Read only its config and prior node outputs.
- Persist node state transitions.
- Respect retry and timeout policy.
- Use existing provider/tool/MCP/artifact/memory store contracts.
- Return deterministic output for edge routing.

Use workflows for reusable orchestration and tools for single capabilities.

## Add Policy Rules

Policy packs define prompt instructions and runtime enforcement rules. Prefer policy packs when behavior must be reusable, auditable, or tenant-specific.

Common enforcement modes include:

- `pre_run`
- `pre_model`
- `post_model`
- `pre_tool`
- `post_tool`
- `pre_artifact_process`
- `post_artifact_process`
- `pre_memory_write`
- `pre_workflow`
- `post_workflow`
- `pre_workflow_node`
- `post_workflow_node`

## Add Knowledge or Memory Integrations

Knowledge and memory extensions should use existing store APIs:

- Knowledge documents and chunks for customer/shared retrieval.
- Memories for stable facts.
- Preferences for conditional choices.
- Embeddings with dimension 768.

Do not store transient conversation history as memory by default.

## Add a Customer Profile Retriever

Use a profile retriever when canonical user data lives outside Duraclaw, such as in a customer database, CRM, identity provider, or Nexus.

The built-in HTTP retriever posts this payload:

```json
{
  "customer_id": "customer-1",
  "user_id": "user-1",
  "agent_instance_id": "agent-1",
  "session_id": "session-1"
}
```

Return a normalized profile envelope:

```json
{
  "profile": {
    "display_name": "Sahal",
    "timezone": "Asia/Jakarta",
    "locale": "id-ID"
  },
  "metadata": {
    "source": "customer_api"
  }
}
```

Duraclaw stores the result in `users.metadata.profile` and records source metadata in `users.metadata.profile_source`.

Keep sensitive fields out of prompt context unless explicitly allowlisted through `DURACLAW_CUSTOMER_PROFILE_PROMPT_FIELDS`.
