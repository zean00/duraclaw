# Operations

## Health and Readiness

```bash
GET /healthz
GET /readyz
```

`/readyz` checks database connectivity and returns queue counts:

- Queued and active runs.
- Pending outbound rows.
- Queued async write jobs.
- Due scheduler jobs.

## Metrics

```bash
GET /metrics
```

The metrics endpoint exposes in-process counters and duration histograms in Prometheus text format. Optional OTLP export can send spans and metrics to an OpenTelemetry collector.

Important metric groups include:

- Run lifecycle and queue lag.
- Model/tool/MCP/artifact processor calls and durations.
- Token usage when providers report it.
- Async write flush/drop/degrade counts.
- Workflow node durations.
- HTTP status counters.

## Durable Debugging

Use durable database records first, then external logs/traces.

Useful routes:

- `GET /admin/observability/events?customer_id={customer_id}`
- `GET /acp/runs/{run_id}/trace`
- `GET /acp/runs/{run_id}/events`
- `GET /acp/runs/{run_id}/background-status`

Run trace output ties together run steps, model calls, tool calls, MCP calls, artifact processor calls, and policy evaluations.

## Workers and Leases

Duraclaw uses PostgreSQL leases and `FOR UPDATE SKIP LOCKED` for:

- Run claiming.
- Scheduler jobs.
- Reminder subscriptions.
- Async writes.
- Outbound outbox rows.
- Idle session monitoring.

If a worker exits, expired leases are recovered by later ticks.

## Retention

```bash
POST /admin/retention/run
```

Retention cleanup can remove old artifacts, events, outbox rows, async write jobs, observability events, and terminal broadcasts. The payload accepts day thresholds such as `artifact_days`, `event_days`, `outbox_days`, `async_write_days`, `observability_days`, and `broadcast_days`.

## Production Checklist

- Use PostgreSQL with `pgvector` installed.
- Set `DURACLAW_REQUIRE_AUTH=true`.
- Configure admin and ACP bearer tokens.
- Use non-mock chat and embedding providers.
- Configure outbound delivery to Nexus.
- Configure `NEXUS_OUTBOUND_BULK_URL` when Nexus supports bulk outbound delivery for broadcasts or reminder fanout.
- Configure media storage for generated artifacts.
- Configure OTLP or scrape `/metrics`.
- Set runtime limits for customers, agent instances, or users.
- Run database backups and retention jobs.

Runtime limits can be set at customer scope or overridden at agent-instance scope. User-scoped limits are also available for model usage budgets. Supported quota fields include run concurrency/queue limits, workflow/background run limits, async-write limits, and model usage budgets:

- `max_daily_tokens`, `max_weekly_tokens`, `max_monthly_tokens`
- `max_daily_model_cost_micros`, `max_weekly_model_cost_micros`, `max_monthly_model_cost_micros`

Model cost quotas use micro-USD integer units. For example, `1000000` means USD 1.00.

Usage summaries are available through `GET /admin/usage/model?customer_id={customer_id}&period=daily|weekly|monthly`, with optional `agent_instance_id` and `user_id` filters.
