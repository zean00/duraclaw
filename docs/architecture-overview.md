# Architecture

Duraclaw uses PostgreSQL as the durability engine. In-memory state is disposable; runs, steps, events, checkpoints, tool calls, MCP calls, model calls, artifacts, workflows, and outbound intents are persisted.

## Main Runtime Flow

```text
ACP StartRun
  -> persist run and user message
  -> worker claims queued run with FOR UPDATE SKIP LOCKED
  -> build safe context
  -> scope judge if the agent profile defines domain scope
  -> process artifacts into representations
  -> build provider prompt
  -> call model
  -> execute tool/MCP/workflow requests
  -> checkpoint after meaningful steps
  -> persist final assistant message
  -> create outbound intent for Nexus
  -> complete run
```

Same-session runs are serialized by the worker claim query. Different sessions may run concurrently, subject to runtime limits.

## Major Subsystems

| Subsystem | Responsibility |
| --- | --- |
| ACP handlers | Nexus-facing sessions, runs, artifacts, events, resume/cancel, outbound status, and admin APIs. |
| Runtime worker | Durable run execution, provider calls, tool loop, workflow handoff, checkpoints, cancellation, and final messages. |
| Providers | OpenAI, OpenRouter, OpenAI-compatible chat, multimodal content, embeddings, streaming contracts, and media generation capabilities. |
| Tools | Local durable tool contracts, argument validation, panic recovery, media generation, memory, preferences, workflow launch, and user clarification. |
| MCP manager | HTTP, SSE, stdio JSON-RPC, persistent stdio, tool/resource/prompt discovery, context propagation, and call persistence. |
| Workflow executor | Durable DAG execution with node states, edge activations, model/tool/MCP/retrieval/memory/artifact/timer/background nodes. |
| Artifacts | Durable input/output media metadata, processor calls, representations, provider-backed processors, and storage adapters. |
| Session monitor | Idle-session compaction, memory/preference extraction, and active-time pattern tracking. |
| Scheduler | PostgreSQL-backed cron jobs, reminder subscriptions, workflow timer wakeups, and deterministic idempotency keys. |
| Outbound | Durable outbound intents for Nexus-owned delivery and delivery status callbacks. |
| Observability | Durable audit events, run traces, counters, `/metrics`, and optional OTLP export. |

## Data Boundary

`customer_id` is the tenant boundary. Most tables carry `customer_id`, and lookups enforce customer scope. Runs snapshot the persisted session agent instance and active agent instance version.

## Detailed Reference

The full architectural reference remains in [Architecture Reference](ARCHITECTURE.md).
