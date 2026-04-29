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
- Configure media storage for generated artifacts.
- Configure OTLP or scrape `/metrics`.
- Set runtime limits for customers or agent instances.
- Run database backups and retention jobs.
