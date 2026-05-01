# Duraclaw

Duraclaw is an ACP-native durable agent runtime. It runs long-lived, resumable assistant work for many customers while [Nexus](https://github.com/zean00/nexus) owns channel adapters and delivery. The core agent loop is copied and adapted from PicoClaw; Duraclaw extends it with ACP-native durability, PostgreSQL persistence, workflows, policy, scheduling, outbound delivery, and multi-tenant runtime controls.

Duraclaw is responsible for:

- Agent instance and session management.
- Durable run execution with PostgreSQL leases and checkpoints.
- Provider calls, tool calls, MCP calls, workflows, and artifact processors.
- Knowledge, memory, preferences, session context, and vector search.
- Reminders, cron fanout, background jobs, broadcasts, and outbound intents for Nexus.
- Policy enforcement, observability, retention, and runtime quotas.

Duraclaw does not contain direct Telegram, WhatsApp, email, or webchat adapters. [Nexus](https://github.com/zean00/nexus) translates channels into ACP HTTP calls and delivers outbound messages.

## System Boundary

```text
Channel adapters
  -> Nexus
    -> ACP over HTTP
      -> Duraclaw
        -> LLM providers
        -> MCP servers
        -> local tools
        -> PostgreSQL + pgvector
```

## Documentation Map

Start here:

- [Installation](installation.md): local Docker, source run, tests, and MkDocs.
- [Configuration](configuration.md): environment variables, providers, OpenRouter model refs, embeddings, processors, MCP, outbox, and monitor settings.
- [Operations](operations.md): readiness, metrics, traces, retention, runtime limits, outbox, scheduler, and test validation.

Core runtime:

- [Architecture](architecture-overview.md): end-to-end runtime flow and subsystem map.
- [Durable Runs](concepts/durable-runs.md): leases, checkpoints, run states, traces, and cancellation.
- [Agent Profiles](concepts/agent-profiles.md): personality, scope judgement, two-pass implicit scope, and recommendation configuration.
- [Workflows](concepts/workflows.md): durable DAG nodes, timers, background jobs, and user clarification.
- [Reminders and Jobs](concepts/reminders-jobs.md): user-scoped reminders, scheduler jobs, and background run management.

Data and integrations:

- [Memory, Preferences, and Knowledge](concepts/memory-preferences-knowledge.md): durable user facts, preferences, session summaries, and retrieval.
- [Artifacts and Media](concepts/artifacts-media.md): multimodal inputs, processors, representations, and generated media.
- [ACP and Admin API](interfaces/api.md): Nexus-facing ACP routes, admin route groups, outbound status, and push delivery.
- [MCP Integration](interfaces/mcp.md): MCP server transports, tools, resources, prompts, and notifications.
- [Extension Guide](extending.md): add providers, tools, MCP servers, processors, workflows, and policies.

Reference:

- [Architecture Reference](ARCHITECTURE.md): long-form design notes and implementation reference.

## Quick Start

```bash
docker compose up --build
```

Or run from source:

```bash
DATABASE_URL=postgres://user:pass@localhost:5432/duraclaw?sslmode=disable \
ADDR=:8080 \
go run ./cmd/duraclaw
```

The service applies embedded migrations on startup and starts the worker, scheduler, session monitor, outbox worker, async writer, and HTTP server.
