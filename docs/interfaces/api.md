# ACP and Admin API

Duraclaw exposes ACP over HTTP for Nexus and admin HTTP routes for configuration and operations.

## Required ACP Headers

Write requests require customer execution context:

```text
X-Customer-ID
X-User-ID
X-Agent-Instance-ID
X-Session-ID
X-Request-ID
X-Idempotency-Key
```

Existing-run write requests also require `X-Run-ID`.

Optional channel headers:

```text
X-Channel-Type
X-Channel-User-ID
X-Channel-Conversation-ID
X-Trace-ID
traceparent
```

## Core ACP Routes

- `GET /acp/agents`
- `PUT /acp/sessions/{session_id}`
- `POST /acp/sessions/{session_id}/reassign`
- `POST /acp/runs`
- `POST /acp/runs/{run_id}/artifacts`
- `POST /acp/runs/{run_id}/artifacts/generate`
- `GET /acp/runs/{run_id}/artifacts`
- `GET /acp/artifacts/{artifact_id}/representations`
- `GET /acp/runs/{run_id}`
- `GET /acp/runs/{run_id}/trace`
- `GET /acp/runs/{run_id}/background-status`
- `GET /acp/runs/{run_id}/events`
- `POST /acp/runs/{run_id}/resume`
- `POST /acp/runs/{run_id}/cancel`
- `POST /acp/outbound-intents/{intent_id}/status`
- `GET /acp/sessions/{session_id}/runs/latest`
- `GET /acp/sessions/{session_id}/runs/by-idempotency-key/{key}`

## Run Input

Simple text:

```json
{"text":"Hello"}
```

Structured content:

```json
{
  "text": "Please inspect this.",
  "parts": [
    {"type": "text", "text": "The receipt is attached."},
    {"type": "artifact_ref", "data": {"artifact_id": "artifact-1"}}
  ]
}
```

Supported part types include `text`, `artifact_ref`, `location`, `structured_data`, and provider multimodal parts such as `image_url`, `file`, `input_audio`, and `video_url`.

## Admin Route Groups

- Agent instance versions and activation.
- Workflows, nodes, edges, and assignments.
- Agent policies, policy packs, rules, assignments, evaluations, and diffs.
- Runtime limits.
- Knowledge ingestion and listing.
- Memories and preferences.
- Reminder subscriptions and scheduler jobs.
- Observability events.
- Outbound intents, broadcasts, and delivery status.
- Background runs.
- MCP server discovery and notifications.
- Retention cleanup.
- Admin media generation.

## Outbound Status

Nexus reports delivery status through:

```text
POST /acp/outbound-intents/{intent_id}/status
```

Accepted statuses:

- `sent_to_nexus`
- `delivered`
- `failed`
- `cancelled`

`sent` is accepted as a compatibility alias for `sent_to_nexus`.

## Duraclaw-to-Nexus Push

When `DURACLAW_OUTBOX_SINK=nexus`, Duraclaw pushes outbound intents to Nexus from the PostgreSQL outbox.

Single endpoint payload is the stored outbound outbox payload:

```json
{
  "outbound_intent_id": "intent-1",
  "customer_id": "customer-1",
  "user_id": "user-1",
  "session_id": "session-1",
  "run_id": "run-1",
  "intent_type": "assistant_message",
  "payload": {
    "text": "Hello"
  }
}
```

Bulk endpoint payload, enabled by `NEXUS_OUTBOUND_BULK_URL`:

```json
{
  "topic": "nexus.outbound_intent",
  "items": [
    {
      "outbox_id": 1,
      "topic": "nexus.outbound_intent",
      "payload": {
        "outbound_intent_id": "intent-1",
        "customer_id": "customer-1",
        "user_id": "user-1",
        "session_id": "session-1",
        "intent_type": "broadcast",
        "payload": {}
      }
    }
  ]
}
```

Both single and bulk requests include:

- `Authorization: Bearer {NEXUS_TOKEN}` when configured.
- `X-Duraclaw-Outbox-Topic`.
- `X-Duraclaw-Outbox-ID` for single sends.
- `X-Duraclaw-Outbox-Batch-Size` for bulk sends.

Duraclaw completes outbox rows only after Nexus returns a 2xx status. Failed sends are released for retry.
