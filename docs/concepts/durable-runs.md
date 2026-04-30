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

## Rapid Follow-Up Refinement

Duraclaw serializes runs per session and also handles rapid follow-up messages without sending stale replies. When a run starts processing, it opens a two-second interrupt window. If Nexus submits another message for the same session during that window, `POST /acp/runs` persists it as a deferred message and returns:

```json
{
  "state": "deferred",
  "active_run_id": "run-a",
  "message_id": "msg-b",
  "deferred_message_id": "defer-b"
}
```

The active run still finishes its internal pipeline, but its direct outbound message is suppressed. Duraclaw stores the draft response, creates a refinement run with the draft plus deferred messages, and emits a `typing` outbound intent so Nexus can keep the chat client warm. The refinement run input includes both structured metadata and prompt-visible text containing the original input, suppressed draft, and deferred follow-up payloads, so the model does not depend on recent-history retention to see the interruption context. Refinement runs use the same configurable window, reset from their own processing start, up to the configured maximum depth. After that, new messages become normal queued follow-up runs.

Deferred run creation is idempotent by `(customer_id, session_id, idempotency_key)`. Duraclaw claims the deferred idempotency row before inserting the nullable-run user message, which prevents duplicate retry requests from leaving extra orphan messages in conversation history.

Completed side effects from a suppressed run are not rolled back. The refinement prompt includes the suppressed draft and tells the agent not to repeat completed tool/workflow side effects unless the user explicitly asks.

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
