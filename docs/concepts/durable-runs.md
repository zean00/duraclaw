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

## Multi-Instance Execution

Multiple Duraclaw instances can process the same database. A run is claimed by atomically selecting a queued row with `FOR UPDATE SKIP LOCKED` and updating its lease owner/expiry. The claim query also refuses to pick a run when another run for the same `(customer_id, session_id)` is active, so one session has at most one active pipeline across all instances.

If an instance crashes while a run is `leased`, `running`, or `running_workflow`, a later worker resets the expired lease back to `queued` and another instance can resume. `awaiting_user` is intentionally not recovered as a crash; it is waiting for user input.

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

The active run still finishes its internal pipeline, but its direct outbound message is suppressed. Duraclaw stores the response as an internal draft, creates a refinement run with the draft plus deferred messages, and emits a `typing` outbound intent so Nexus can keep the chat client warm. The refinement run input includes both structured metadata and prompt-visible text containing the original input, suppressed draft, completed parent tool artifacts, and deferred follow-up payloads, so the model does not depend on recent-history retention to see the interruption context. Refinement runs use the same configurable window, reset from their own processing start, up to the configured maximum depth. After that, new messages become normal queued follow-up runs.

Deferred run creation is idempotent by `(customer_id, session_id, idempotency_key)`. Duraclaw claims the deferred idempotency row before inserting the nullable-run user message, which prevents duplicate retry requests from leaving extra orphan messages in conversation history.

Completed side effects from a suppressed run are not rolled back. The refinement prompt includes the suppressed draft and completed parent artifacts, tells the agent to update recent side-effect artifacts instead of duplicating them, and tells the final response to describe the completed result with the latest details rather than exposing hidden draft/update mechanics.

## Agent Activity Signals

Run internals are always durable through run events, run steps, model calls, tool calls, MCP calls, workflow records, and outbound intents. For realtime UX, Duraclaw can also emit configurable `agent_activity` outbound intents while a run is processing. Nexus can translate these into channel-appropriate indicators such as “thinking”, “checking policy”, “calling tool lookup”, or “running workflow”.

Activity emission is controlled by `DURACLAW_AGENT_ACTIVITY_ENABLED`, `DURACLAW_AGENT_ACTIVITY_INCLUDE`, and `DURACLAW_AGENT_ACTIVITY_OMIT`. Supported activity types are `thinking`, `scope`, `context`, `workflow`, `model`, `tool`, `artifact`, and `refinement`. The omit list wins over the include list.

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
