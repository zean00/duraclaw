# Durable Runs

Every user turn, cron trigger, reminder, background job, workflow timer, and broadcast-generation task is represented as a durable run.

## Run States

- `queued`
- `leased`
- `running`
- `running_workflow`
- `awaiting_user`
- `completed`
- `failed`
- `cancelled`
- `expired`

## Execution Path

```text
queued
  -> leased
  -> running
  -> optional workflow
  -> context build
  -> scope judge
  -> artifact processing
  -> model/tool loop
  -> final assistant message
  -> completed
```

The worker persists:

- Run state transitions.
- Run steps.
- Run events.
- Checkpoints.
- Model call intent and result summaries.
- Tool call intent and result summaries.
- MCP call intent and result summaries.
- Artifact processor call intent and results.
- Final assistant message.

## Idempotency

Run creation uses `(customer_id, session_id, idempotency_key)` to prevent duplicate work. Scheduler and reminder jobs use deterministic idempotency keys based on the job/subscription and scheduled fire time.

## Cancellation and Awaiting User

Workers check run state during execution. A cancelled run stops without completing further side effects.

`awaiting_user` is used for clarification flows:

```text
agent or workflow asks user
  -> run becomes awaiting_user
  -> Nexus asks the user
  -> Nexus calls ACP resume
  -> Duraclaw continues the same run
```

## Background Runs

Background jobs reuse the same durable run pipeline with `run_mode='background'`. They support progress storage, cancellation, status APIs, quotas, and completion outbound intents.
