# ACP and Admin API

Duraclaw exposes ACP over HTTP for [Nexus](https://github.com/zean00/nexus) and admin HTTP routes for configuration and operations.

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

Channel headers are persisted on the run and exposed to the agent prompt context so profile instructions, policies, recommendations, and workflows can adapt response style or behavior per channel.

`location` content parts are normalized into trusted runtime context for prompts, policies, recommendations, and workflows. Use numeric `latitude`/`longitude` or aliases `lat`/`lng`, plus optional `label`.

## Core ACP Routes

Session and runs:

- `GET /acp/agents`
- `PUT /acp/sessions/{session_id}`
- `POST /acp/sessions/{session_id}/reassign`
- `POST /acp/runs`
- `GET /acp/runs/{run_id}`
- `GET /acp/runs/{run_id}/trace`
- `GET /acp/runs/{run_id}/background-status`
- `GET /acp/runs/{run_id}/events`
- `POST /acp/runs/{run_id}/resume`
- `POST /acp/runs/{run_id}/cancel`
- `GET /acp/sessions/{session_id}/runs/latest`
- `GET /acp/sessions/{session_id}/runs/by-idempotency-key/{key}`

Artifacts:

- `POST /acp/runs/{run_id}/artifacts`
- `POST /acp/runs/{run_id}/artifacts/generate`
- `GET /acp/runs/{run_id}/artifacts`
- `GET /acp/artifacts/{artifact_id}/representations`

User-scoped scheduled work:

- `POST /acp/reminders`
- `GET /acp/reminders?customer_id={customer_id}&user_id={user_id}`
- `PATCH /acp/reminders/{subscription_id}`
- `DELETE /acp/reminders/{subscription_id}?customer_id={customer_id}&user_id={user_id}`
- `POST /acp/scheduler/jobs`
- `GET /acp/scheduler/jobs?customer_id={customer_id}&user_id={user_id}`
- `PATCH /acp/scheduler/jobs/{job_id}`
- `DELETE /acp/scheduler/jobs/{job_id}?customer_id={customer_id}&user_id={user_id}`
- `GET /acp/background-runs?customer_id={customer_id}&user_id={user_id}`
- `POST /acp/background-runs/{run_id}/cancel`

Outbound:

- `POST /acp/outbound-intents/{intent_id}/status`

See [Reminders, Scheduler Jobs, And Background Runs](../concepts/reminders-jobs.md) for payloads and user-scoping rules.

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
- Runtime limits and model usage summaries.
- Knowledge ingestion and listing.
- Memories and preferences.
- Reminder subscriptions and scheduler jobs.
- Observability events.
- Outbound intents, broadcasts, and delivery status.
- Background runs.
- Manual session compaction.
- Built-in tool access rules by customer, agent instance, and user.
- MCP server discovery and notifications.
- MCP tool access rules by customer, agent instance, user, and server.
- Retention cleanup.
- Admin media generation.

Agent instance config import/export:

- `POST /admin/agent-instances/{agent_instance_id}/versions/import?format=json|yaml`
- `GET /admin/agent-instances/{agent_instance_id}/versions/{version_id}/export?format=json|yaml`

Admin knowledge routes:

- `POST /admin/knowledge/text`
- `GET /admin/knowledge/documents?customer_id={customer_id}&scope=customer|shared|all&limit=100`
- `GET /admin/knowledge/documents/{document_id}/chunks?limit=100`
- `DELETE /admin/knowledge/documents/{document_id}?customer_id={customer_id}`

Knowledge ingestion request:

```json
{
  "customer_id": "customer-1",
  "scope": "shared",
  "title": "Prayer time policy",
  "source_ref": "manual:prayer-policy-v1",
  "text": "Long knowledge text to chunk and retrieve later.",
  "metadata": {"category": "religious_service"}
}
```

Use `scope: "customer"` for customer-specific knowledge and `scope: "shared"` for knowledge retrievable by all customers. Listing accepts `scope=customer`, `scope=shared`, or `scope=all`; retrieval uses both current customer knowledge and shared knowledge.

Admin recommendation routes:

- `POST /admin/recommendations/items`
- `GET /admin/recommendations/items?customer_id={customer_id}`
- `PATCH /admin/recommendations/items/{item_id}`
- `DELETE /admin/recommendations/items/{item_id}?customer_id={customer_id}`
- `GET /admin/recommendations/decisions?customer_id={customer_id}`
- `GET /admin/recommendations/jobs?customer_id={customer_id}`

Recommendation item request:

```json
{
  "customer_id": "customer-1",
  "kind": "activity",
  "title": "Family weekend class",
  "description": "A kid-friendly weekend activity.",
  "tags": ["family", "weekend"],
  "url": "https://example.com/activity",
  "priority": 10,
  "sponsored": true,
  "sponsor_name": "Example Partner",
  "status": "active",
  "valid_from": "2026-05-01T00:00:00Z",
  "valid_until": "2026-06-01T00:00:00Z",
  "metadata": {"city": "Jakarta"}
}
```

Patch accepts the same fields as optional updates plus required `customer_id`. Decision and job routes are read-only audit/operations views for recommendation selection and timeout fallback processing.

Manual session compaction:

- `POST /admin/sessions/{session_id}/compact`

Request:

```json
{
  "customer_id": "customer-1",
  "force": true,
  "message_limit": 80
}
```

Response includes the generated durable summary:

```json
{
  "customer_id": "customer-1",
  "session_id": "session-1",
  "summary": "Durable summary text...",
  "compacted": true,
  "message_count": 42,
  "transcript_chars": 12000,
  "provider": "openrouter",
  "model": "openai/gpt-4.1-mini",
  "metadata": {"strategy": "manual_llm_compaction", "forced": true}
}
```

When `force` is false, the endpoint only compacts if the transcript reaches `DURACLAW_SESSION_COMPACTION_THRESHOLD_CHARS`; otherwise it returns `compacted: false` with an empty summary.

Built-in tool access routes:

- `PUT /admin/tool-access/customers/{customer_id}/agent-instances/{agent_instance_id}`
- `GET /admin/tool-access/customers/{customer_id}/agent-instances/{agent_instance_id}`
- `DELETE /admin/tool-access/customers/{customer_id}/agent-instances/{agent_instance_id}`
- `PUT /admin/tool-access/customers/{customer_id}/agent-instances/{agent_instance_id}/users/{user_id}`
- `GET /admin/tool-access/customers/{customer_id}/agent-instances/{agent_instance_id}/users/{user_id}`
- `DELETE /admin/tool-access/customers/{customer_id}/agent-instances/{agent_instance_id}/users/{user_id}`

Payload:

```json
{
  "allowed_tools": ["remember", "list_memories", "duraclaw.run_workflow"],
  "denied_tools": ["echo", "duraclaw.generate_video"],
  "metadata": {}
}
```

If no rule exists, built-in tools follow the agent instance version `tool_config`. A customer/agent rule narrows the baseline for that agent instance. A user rule replaces the customer/agent rule for that user. `denied_tools` wins over `allowed_tools`; when `allowed_tools` is empty, every built-in tool is allowed except denied tools.

`tool_config.max_tool_calls_per_run` applies to both built-in tools and directly exposed MCP function tools.

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
  "run_id": "intent-1",
  "durable_run_id": "run-1",
  "intent_type": "assistant_message",
  "payload": {
    "text": "Hello"
  }
}
```

`run_id` is intentionally unique per outbound intent for Nexus delivery idempotency. The original Duraclaw durable run is preserved as `durable_run_id`.

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
