# Operations

## Health and Readiness

```bash
GET /healthz
GET /readyz
```

`/readyz` checks database connectivity and returns queue counts:

- Queued and active runs.
- Pending outbound rows, split into unclaimed, currently claimed, and stale claimed rows.
- Queued async write jobs.
- Due scheduler jobs.

## Metrics

```bash
GET /metrics
```

The metrics endpoint exposes in-process counters and duration histograms in Prometheus text format. Optional OTLP export can send spans and metrics to an OpenTelemetry collector.

## Outbox Delivery

Duraclaw writes every outbound intent to `outbound_intents` and `async_outbox`. Automatic Nexus delivery requires a running Duraclaw process with the outbox worker enabled and `DURACLAW_OUTBOX_SINK=nexus` or `http`.

Use `/readyz` to distinguish common local failures:

- `outbox_pending > 0` and `outbox_unclaimed > 0`: no worker is draining rows, or rows are waiting for retry.
- `outbox_claimed > 0`: a worker has claimed rows and is delivering or stuck in a sink call.
- `outbox_stale > 0`: a previous worker likely stopped after claiming rows; another worker can reclaim them.

The outbox worker logs sink failures as `outbox delivery failed` and releases rows for retry after the configured retry delay. If a process is killed while rows are claimed, they become retryable after the database claim lease expires.

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

High-volume, non-critical telemetry is buffered through `async_write_jobs` when the async writer is enabled. This includes streaming model delta run events, agent activity run events, scope-judge audit events, prompt-injection block telemetry, checkpoint sidecar observability payloads, and optional OTLP sidecars. Critical durability records remain synchronous: run state, checkpoints, model/tool/MCP call intent and completion records, policy denials, quota failures, awaiting-user transitions, outbox writes, and final responses.

## Workers and Leases

Duraclaw is safe to run as multiple instances against one shared PostgreSQL database. PostgreSQL is the coordination layer; do not add per-process queues or in-memory locks around durable work.

Duraclaw uses PostgreSQL row locks, leases, and `FOR UPDATE SKIP LOCKED` for:

- Run claiming.
- Scheduler jobs.
- Reminder subscriptions.
- Shared scheduler jobs.
- Shared scheduler durable-run delivery fanout.
- Async writes.
- Outbound outbox rows.
- Idle session monitoring.

Runs are additionally serialized per `(customer_id, session_id)`: a queued run is not claimable while another run in that session is `leased`, `running`, `running_workflow`, or `awaiting_user`. Different sessions can run concurrently across instances.

If a worker exits, expired run, scheduler, reminder, shared-scheduler, async-write, recommendation, and session-monitor leases are recovered by later ticks. Outbound outbox claims also have a database lease that the outbox worker renews while a sink call is active; rows are only reclaimed after that lease expires. Shared-scheduler durable-run fanout and generated-broadcast fanout use stale `processing` recovery so another scheduler tick can finish outbox creation after a crash.

Operational caveats:

- All instances must use the same database and migrations.
- Use stable, distinct worker owner names (`HOSTNAME` is used by default) for observability.
- Recovery is not instant; it happens after the configured lease or stale-claim window expires.
- External side-effecting tools and MCP servers should be idempotent when possible because a crash after an external side effect but before local completion can cause a recovered run to retry.

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
