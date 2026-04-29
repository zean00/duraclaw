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
