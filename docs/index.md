# Duraclaw

Duraclaw is an ACP-native durable agent runtime. It runs long-lived, resumable assistant work for many customers while Nexus owns channel adapters and delivery.

Duraclaw is responsible for:

- Agent instance and session management.
- Durable run execution with PostgreSQL leases and checkpoints.
- Provider calls, tool calls, MCP calls, workflows, and artifact processors.
- Knowledge, memory, preferences, session context, and vector search.
- Reminders, cron fanout, background jobs, broadcasts, and outbound intents for Nexus.
- Policy enforcement, observability, retention, and runtime quotas.

Duraclaw does not contain direct Telegram, WhatsApp, email, or webchat adapters. Nexus translates channels into ACP HTTP calls and delivers outbound messages.

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

- Start with [Installation](installation.md) for local Docker and binary setup.
- Use [Configuration](configuration.md) for environment variables and provider setup.
- Read [Architecture](architecture-overview.md) for the end-to-end runtime shape.
- Read [Agent Profiles](concepts/agent-profiles.md), [Durable Runs](concepts/durable-runs.md), and [Workflows](concepts/workflows.md) for core concepts.
- Use [ACP and Admin API](interfaces/api.md) when integrating Nexus or admin tooling.
- Use [Extension Guide](extending.md) to add providers, tools, MCP servers, processors, workflows, and policies.

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
