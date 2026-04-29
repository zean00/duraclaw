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
