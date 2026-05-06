# Reminders, Scheduler Jobs, And Background Runs

Duraclaw has three durable primitives for work that happens outside a normal immediate chat response:

- Reminder subscriptions: user-facing cron-like reminder subscriptions, including `@once`.
- Scheduler jobs: lower-level one-time or recurring run triggers for research jobs, checks, workflows, and other background work.
- Shared scheduler jobs: customer-level polling jobs with user subscriptions and dynamic external eligibility, such as location-dependent prayer reminders.
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

Reminder subscriptions are durable cron-like subscriptions. `schedule` accepts cron expressions, `@once`, or `@interval`. If `next_run_at` is omitted for a cron expression, Duraclaw computes the next fire time from `schedule`.

Bounded recurring reminders use `@interval` with a positive `repeat_interval_seconds` and an absolute `next_run_at` first fire time. Optional `repeat_until` and `repeat_count` stop future fires. `repeat_until` must be after the effective `next_run_at`; if an already-claimed due fire is at or past `repeat_until`, the scheduler disables the subscription without creating a reminder run.

Reminder workers are multi-instance safe. Due subscriptions are claimed with PostgreSQL row locks and expiring leases. Each fire creates a durable run with a deterministic idempotency key derived from the subscription and scheduled fire time, so a retry after worker crash reuses the same run instead of creating a duplicate.

Reminder subscriptions can optionally set `channel_type`. If it is omitted, the due reminder outbound intent is channel-neutral and Nexus fans it out to every active channel session mapped to the same ACP session. If it is set, Duraclaw propagates that channel preference through the reminder run and outbound intent, and Nexus delivers only to mapped sessions for that channel.

Agents can manage reminders from the model loop through built-in tools. `create_reminder` persists a reminder subscription for the current customer/user/session and returns a typed `reminder_reference` artifact in the tool result. `update_reminder` accepts the `subscription_id` from a recent `reminder_reference` and updates the existing reminder instead of creating a duplicate.

Reminder tools are intentionally conservative. The agent should ask for clarification before creating a reminder when the date/time is ambiguous, and rapid follow-ups such as "at 8am" should update the recent reminder reference when one exists. In rapid-follow-up refinement, the suppressed first response is only an internal draft; if the tool updates that draft-side reminder, the final response should still tell the user the reminder was set with the latest details. The reference contains the `subscription_id`, schedule metadata, and the ACP/admin management routes needed to pause, resume, update, or delete that specific reminder.

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

Bounded interval example:

```json
{
  "customer_id": "customer-1",
  "user_id": "user-1",
  "session_id": "session-1",
  "agent_instance_id": "agent-1",
  "title": "Minum obat",
  "schedule": "@interval",
  "timezone": "Asia/Jakarta",
  "next_run_at": "2026-04-29T01:00:00Z",
  "repeat_interval_seconds": 28800,
  "repeat_until": "2026-05-02T01:00:00Z",
  "payload": {"text": "Pengingat: minum obat."}
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

## Shared Scheduler Jobs

Shared scheduler jobs are customer-level jobs that evaluate a subscriber set on each schedule tick. They are useful when many users subscribe to one logical reminder, but actual eligibility depends on external data such as location-specific prayer times.

Shared scheduler workers are also multi-instance safe. The shared job row is claimed with an expiring lease; per-fire records are unique by `(shared_job_id, scheduled_fire_at)`, and fanout deliveries are unique by `(shared_job_id, scheduled_fire_at, user_id, action)`. If a worker crashes mid-fanout, later ticks can retry after lease/stale-processing expiry, and already-created delivery rows are skipped by the database uniqueness constraints.

Admin job routes:

- `POST /admin/shared-scheduler/jobs`
- `GET /admin/shared-scheduler/jobs?customer_id={customer_id}&limit=100`
- `PATCH /admin/shared-scheduler/jobs/{job_id}`
- `DELETE /admin/shared-scheduler/jobs/{job_id}?customer_id={customer_id}`

User subscription routes:

- `POST /acp/shared-scheduler/subscriptions`
- `GET /acp/shared-scheduler/subscriptions?customer_id={customer_id}&user_id={user_id}&shared_job_id={job_id}`
- `PATCH /acp/shared-scheduler/subscriptions/{subscription_id}`
- `DELETE /acp/shared-scheduler/subscriptions/{subscription_id}?customer_id={customer_id}&user_id={user_id}`

Example shared prayer job:

```json
{
  "customer_id": "customer-1",
  "job_key": "prayer_reminder",
  "title": "Prayer reminder",
  "job_type": "eligibility_poll",
  "schedule": "*/1 * * * *",
  "timezone": "Asia/Jakarta",
  "fanout_action": "outbound_intent",
  "message_template": "It is time for {{prayer_name}} prayer.",
  "external_service": {
    "url": "https://example.com/prayer/due",
    "method": "POST",
    "headers": {"Authorization": "Bearer secret"},
    "include_subscribers": true,
    "response_mapping": {
      "target_path": "result.users",
      "target_shape": "list",
      "id_path": "user_id"
    }
  }
}
```

The scheduler posts job context to `external_service`. It only includes active subscriber records when `include_subscribers` is explicitly `true`; keep this disabled for large subscriber sets and have the external service return eligible Duraclaw `user_id`s instead. The response mapping can read user IDs from a list path such as `users` or `result.users`, from object keys, or from object values. A missing mapped user-list path falls back to all active subscribers; a present-but-empty mapped list selects no recipients. If no external service is configured, the job also fans out to all active subscribers.

`fanout_action` controls execution cost. `outbound_intent` creates one direct message intent per selected subscriber. `durable_run` creates one durable agent run per selected `agent_instance_id`, not one run per subscriber; the run input contains the selected subscriber list so the agent can generate a profile-consistent shared reminder. When that durable run completes, the shared scheduler claims the completed run result and creates one outbound intent per selected subscriber. The reminder system remains responsible for subscriber outbox fanout.

For `durable_run`, the durable run itself does not send subscriber outbox messages directly. The shared scheduler claims the completed run delivery and creates one outbound intent per selected subscriber. Completed delivery fanout uses row locks plus stale `processing` recovery so another instance can finish fanout if a scheduler process crashes after claiming the completed run.

Example subscription:

```json
{
  "customer_id": "customer-1",
  "user_id": "user-1",
  "shared_job_id": "job-1",
  "session_id": "session-1",
  "agent_instance_id": "agent-1",
  "subscriber_metadata": {
    "location": {"latitude": -6.2, "longitude": 106.8, "label": "Jakarta"},
    "timezone": "Asia/Jakarta"
  }
}
```

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

Admin patch accepts `customer_id` plus `enabled` for pause/resume. To update user-owned fields such as `channel_type`, include `user_id` and the same mutable fields supported by the ACP user-scoped patch route.
- `POST /admin/scheduler/jobs`
- `GET /admin/scheduler/jobs?customer_id={customer_id}`
- `PATCH /admin/scheduler/jobs/{job_id}`
- `GET /admin/background-runs?customer_id={customer_id}`
