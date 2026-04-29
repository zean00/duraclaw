# Reminders, Scheduler Jobs, And Background Runs

Duraclaw has three durable primitives for work that happens outside a normal immediate chat response:

- Reminder subscriptions: user-facing cron-like reminder subscriptions, including `@once`.
- Scheduler jobs: lower-level one-time or recurring run triggers for research jobs, checks, workflows, and other background work.
- Background runs: durable runs created for long-running or asynchronous work, with user-scoped listing and cancellation.

Nexus remains responsible for channel delivery. Duraclaw persists reminders/jobs/runs and emits outbound intents for Nexus to deliver.

## User Scope

ACP user-scoped routes enforce `X-Customer-ID` and `X-User-ID` against the requested `customer_id` and `user_id`. If either value does not match, the route returns not found instead of exposing cross-user data.

Most ACP write requests also require normal execution headers:

```text
X-Customer-ID
X-User-ID
X-Agent-Instance-ID
X-Session-ID
X-Request-ID
X-Idempotency-Key
```

## Reminder Subscriptions

Reminder subscriptions are durable cron-like subscriptions. `schedule` accepts cron expressions or `@once`. If `next_run_at` is omitted, Duraclaw computes the next fire time from `schedule`.

Routes:

- `POST /acp/reminders`
- `GET /acp/reminders?customer_id={customer_id}&user_id={user_id}&limit=100`
- `PATCH /acp/reminders/{subscription_id}`
- `DELETE /acp/reminders/{subscription_id}?customer_id={customer_id}&user_id={user_id}`

Create body:

```json
{
  "customer_id": "customer-1",
  "user_id": "user-1",
  "session_id": "session-1",
  "agent_instance_id": "agent-1",
  "title": "Minum obat malam",
  "schedule": "@once",
  "timezone": "Asia/Jakarta",
  "next_run_at": "2026-04-29T20:00:00Z",
  "payload": {"text": "Pengingat: minum obat malam."},
  "metadata": {"origin": "conversation"}
}
```

Patch body supports these user-owned fields:

- `title`
- `schedule`
- `timezone`
- `payload`
- `next_run_at`
- `metadata`
- `enabled`

## Scheduler Jobs

Scheduler jobs are lower-level durable run triggers. Use them for one-time jobs, recurring jobs, research tasks, workflow triggers, and system-created background work. When due, the scheduler creates a durable run from the stored `input`.

Routes:

- `POST /acp/scheduler/jobs`
- `GET /acp/scheduler/jobs?customer_id={customer_id}&user_id={user_id}&limit=100`
- `PATCH /acp/scheduler/jobs/{job_id}`
- `DELETE /acp/scheduler/jobs/{job_id}?customer_id={customer_id}&user_id={user_id}`

Create body:

```json
{
  "customer_id": "customer-1",
  "user_id": "user-1",
  "agent_instance_id": "agent-1",
  "session_id": "session-1",
  "job_type": "research",
  "schedule": "@once",
  "next_run_at": "2026-04-29T13:00:00Z",
  "input": {"text": "Research this topic and summarize the result."},
  "metadata": {"source": "conversation"}
}
```

Patch body supports these user-owned fields:

- `schedule`
- `next_run_at`
- `input`
- `metadata`
- `enabled`

## Background Runs

Background runs are normal durable runs marked for asynchronous work. User-scoped management supports listing and cancellation:

- `GET /acp/background-runs?customer_id={customer_id}&user_id={user_id}&agent_instance_id={agent_instance_id}&limit=100`
- `POST /acp/background-runs/{run_id}/cancel`

Cancel body:

```json
{
  "customer_id": "customer-1",
  "user_id": "user-1"
}
```

## Admin Routes

Admin routes are customer-wide and are intended for internal tooling:

- `POST /admin/reminders/subscriptions`
- `GET /admin/reminders/subscriptions?customer_id={customer_id}`
- `PATCH /admin/reminders/subscriptions/{subscription_id}`
- `POST /admin/scheduler/jobs`
- `GET /admin/scheduler/jobs?customer_id={customer_id}`
- `PATCH /admin/scheduler/jobs/{job_id}`
- `GET /admin/background-runs?customer_id={customer_id}`
