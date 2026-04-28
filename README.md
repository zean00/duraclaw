# Duraclaw

Duraclaw is an ACP-native durable agent runtime. Nexus owns channel adapters and calls Duraclaw over HTTP.

## Run

```bash
DATABASE_URL=postgres://user:pass@localhost:5432/duraclaw?sslmode=disable \
ADDR=:8080 \
go run ./cmd/duraclaw
```

Local Docker stack:

```bash
docker compose up --build
```

The service applies embedded SQL migrations on startup, starts the durable run worker, starts the scheduler loop, and exposes ACP routes under `/acp`.
It also starts an async outbox worker with a placeholder sink; Nexus delivery can replace that sink without changing runtime persistence.

Provider configuration defaults to the built-in mock provider. To use an OpenAI-compatible `/chat/completions` endpoint:

```bash
DURACLAW_PROVIDER=openai-compatible
DURACLAW_PROVIDER_BASE_URL=https://api.openai.com/v1
DURACLAW_PROVIDER_API_KEY=...
DURACLAW_PROVIDER_MODEL=gpt-4.1-mini
DURACLAW_PROVIDER_FALLBACKS=mock/duraclaw
```

Outbox delivery defaults to a placeholder log sink. To push queued outbound intents to Nexus:

```bash
DURACLAW_OUTBOX_SINK=nexus
NEXUS_OUTBOUND_URL=http://nexus.internal/acp/outbound
NEXUS_TOKEN=...
```

Admin endpoints are open by default for local development. Set this in shared or production environments:

```bash
DURACLAW_ADMIN_TOKEN=...
```

PostgreSQL must have `pgcrypto` and `pgvector` available. The first migration runs:

```sql
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS vector;
```

## Test

```bash
go test ./...
```

Optional real PostgreSQL migration verification:

```bash
TEST_DATABASE_URL=postgres://user:pass@localhost:5432/duraclaw_test?sslmode=disable \
go test ./internal/db -run TestMigrateAgainstPostgres
```

## ACP Routes

- `GET /acp/agents`
- `GET /healthz`
- `GET /readyz`
- `GET /metrics`
- `POST /admin/agent-instances/{agent_instance_id}/versions`
- `GET /admin/agent-instances/{agent_instance_id}/versions?customer_id={customer_id}`
- `POST /admin/agent-instances/{agent_instance_id}/versions/{version_id}/activate`
- `POST /admin/workflows`
- `GET /admin/workflows`
- `GET /admin/workflows/{workflow_id}/nodes`
- `PUT /admin/workflows/{workflow_id}/nodes/{node_key}`
- `GET /admin/workflows/{workflow_id}/edges`
- `PUT /admin/workflows/{workflow_id}/edges`
- `POST /admin/workflows/{workflow_id}/assignments`
- `PUT /admin/agent-policies`
- `GET /admin/agent-policies?customer_id={customer_id}&agent_instance_id={agent_instance_id}`
- `POST /admin/policy-packs`
- `GET /admin/policy-packs`
- `PUT /admin/policy-packs/{pack_id}/rules/{rule_id}`
- `GET /admin/policy-packs/{pack_id}/rules`
- `POST /admin/policy-packs/{pack_id}/assignments`
- `GET /admin/policy-evaluations?run_id={run_id}`
- `POST /admin/knowledge/text`
- `GET /admin/knowledge/documents?customer_id={customer_id}`
- `GET /admin/knowledge/documents/{document_id}/chunks`
- `DELETE /admin/knowledge/documents/{document_id}?customer_id={customer_id}`
- `POST /admin/memories`
- `GET /admin/memories?customer_id={customer_id}&user_id={user_id}`
- `PUT /admin/memories/{memory_id}`
- `DELETE /admin/memories/{memory_id}?customer_id={customer_id}&user_id={user_id}`
- `POST /admin/preferences`
- `GET /admin/preferences?customer_id={customer_id}&user_id={user_id}`
- `PUT /admin/preferences/{preference_id}`
- `DELETE /admin/preferences/{preference_id}?customer_id={customer_id}&user_id={user_id}`
- `POST /admin/reminders/subscriptions`
- `GET /admin/reminders/subscriptions?customer_id={customer_id}`
- `PATCH /admin/reminders/subscriptions/{subscription_id}`
- `POST /admin/scheduler/jobs`
- `GET /admin/scheduler/jobs?customer_id={customer_id}`
- `PATCH /admin/scheduler/jobs/{job_id}`
- `GET /admin/observability/events?customer_id={customer_id}`
- `GET /admin/outbound-intents?customer_id={customer_id}`
- `POST /admin/broadcasts`
- `GET /admin/broadcasts?customer_id={customer_id}`
- `GET /admin/broadcasts/{broadcast_id}/targets?customer_id={customer_id}`
- `POST /admin/broadcasts/{broadcast_id}/cancel`
- `POST /admin/retention/run`
- `PUT /acp/sessions/{session_id}`
- `POST /acp/sessions/{session_id}/reassign`
- `POST /acp/runs`
- `POST /acp/runs/{run_id}/artifacts`
- `GET /acp/runs/{run_id}/artifacts`
- `GET /acp/artifacts/{artifact_id}/representations`
- `GET /acp/runs/{run_id}`
- `GET /acp/runs/{run_id}/trace`
- `GET /acp/runs/{run_id}/events`
- `POST /acp/runs/{run_id}/resume`
- `POST /acp/runs/{run_id}/cancel`
- `POST /acp/outbound-intents/{intent_id}/status`
- `GET /acp/sessions/{session_id}/runs/latest`
- `GET /acp/sessions/{session_id}/runs/by-idempotency-key/{key}`

Write requests require customer execution context headers. Existing-run write requests also require `X-Run-ID` matching the route run id.
Run status, event, trace, artifact, and representation reads require `X-Customer-ID` for tenant scoping.
Outbound intent status callbacks also require `X-Customer-ID`.
Run input may include `text` and a `parts` array. Supported part types are `text`, `artifact_ref`, `location`, and `structured_data`; `artifact_ref` parts must include `data.artifact_id`.
Run input may also include `workflow_id` or `workflow_definition_id` to execute an assigned workflow graph before the normal agent loop.

Workflow graph execution v1 is a durable DAG runner. It persists node states and edge activations, runs dependency-ready nodes concurrently with a default limit of 4, and treats merge nodes as `all_active` joins.

Supported node types:

- `start`, `checkpoint`, `message`, and `end`: complete immediately and persist node output.
- `split`: activates all matching outgoing edges.
- `merge`: waits until all active upstream parents succeed.
- `switch`: copies a configured key into `route`.
- `condition`: routes with deterministic `equals` or `not_equals` config.
- `llm_condition`: persists a model call and requires JSON output containing `route`.
- `tool` and `tool_call`: execute a local durable tool and persist tool intent/result.
- `mcp` and `mcp_call`: execute an MCP tool and persist MCP intent/result.
- `model_call`: executes a provider model call and can require strict JSON output.
- `retrieve_knowledge`: reads matching customer knowledge chunks.
- `read_memory` and `write_memory`: read or persist stable user facts; writes pass policy enforcement.
- `read_preference` and `write_preference`: read or persist conditional preferences with condition metadata.
- `read_artifact`, `process_artifact`, and `write_artifact`: operate on durable artifact metadata and representations.
- `branch`: emits a route from a configured key.
- `transform`: maps constants and prior output values into a new output object.
- `loop`: performs bounded node-internal iteration over an array.
- `wait_timer`: creates a one-shot scheduler wake job and pauses the workflow until the timer resumes the node.
- `emit_outbound_message`: creates an outbound intent for Nexus delivery.
- `create_background_job`: creates a queued durable run.
- `ask_user`: persists the workflow and parent run as `awaiting_user`; `POST /acp/runs/{run_id}/resume` continues from the next edge.

Node retry policy supports `{"max_attempts":2}` or `{"attempts":2}`. Timeout policy supports `{"seconds":30}` or `{"timeout_seconds":30}`. Exhausted retries fail the workflow and parent run.

Workflow edges are deterministic. Empty conditions always match. Supported edge conditions:

```json
{"equals":{"key":"text","value":"go"}}
{"not_equals":{"key":"status","value":"failed"}}
```

Create a durable cron-backed run trigger:

```bash
curl -X POST http://localhost:8080/admin/scheduler/jobs \
  -H 'Content-Type: application/json' \
  -d '{
    "customer_id": "customer-1",
    "user_id": "user-1",
    "agent_instance_id": "agent-1",
    "session_id": "session-1",
    "schedule": "*/15 * * * *",
    "input": {"text": "Run the scheduled check."}
  }'
```

## Current Runtime Slice

The implemented durable path is:

```text
queued run
  -> lease with FOR UPDATE SKIP LOCKED
  -> running
  -> optional workflow graph execution
  -> bounded model/tool/workflow iterations
  -> policy evaluation and run steps/checkpoints
  -> artifact processor intent/result records
  -> model call intent/result records
  -> tool intent/result records when requested, with non-retryable result suppression
  -> optional workflow or agent awaiting-user state
  -> final assistant message
  -> outbound intent queued for Nexus delivery
  -> completed
```

Scheduler jobs, reminder subscriptions, and async outbox records also use PostgreSQL claim queries with `FOR UPDATE SKIP LOCKED`.

The persistence layer also includes:

- Agent instance versions with active-version pointers, per-run version snapshots, versioned system instructions, and model fallback overrides.
- Workflow definitions, nodes, edges, assignments, workflow runs, node states, edge activations, and workflow node runs.
- Policy packs, rules, assignments, and policy evaluation audit records.
- Session agent-instance transfer records; session ensure and run creation use the persisted session agent unless the explicit reassignment route changes it.
- Memory records for stable facts scoped by customer/user/session.
- Preference records for conditional preferences scoped by customer/user/session.
- Reminder subscriptions fan out into deterministic durable runs; one-shot workflow timer wake jobs are backed by scheduler jobs.
- Knowledge documents and vector-ready chunks with pgvector search helpers.
- Outbound intents queued through `async_outbox` for Nexus-owned delivery.
- Broadcast creation creates per-target outbound intents for Nexus-owned delivery.
- Recent session message history used by the worker when composing model context.
- Latest session transfer note included in model context after reassignment.
- Context compaction for bounded prompt history.
- Artifact attachment policy checks for size, allowed media types, and raw payload metadata fields.
- Memory tools: `remember` and `list_memories` for stable facts.
- Preference tools: `save_preference` and `list_preferences` for conditional preferences.
- Non-retryable tool metadata for write-like tools, with recovery query helpers.
- Admin text knowledge ingestion into deterministic chunks.
- Optional embedding provider seam for knowledge chunk embeddings, with deterministic hash embeddings for tests/local use.
- Artifact processor provider-adapter seam for OCR/transcription/document extraction adapters.
- Retention maintenance hooks for old events, completed outbox rows, and artifact expiry.
- Retention maintenance hooks for observability events and terminal broadcasts.
- Lease extension and cancellation checks during worker execution.
- Retry release/backoff for failed async outbox delivery.
- Provider registry and model fallback attempts with persisted model-call records per attempt.
- Artifact processor registry for modality-specific processor selection.
- MCP manager registration now tracks transport metadata, opt-in retry attempts, per-server status, max-concurrency limits, HTTP/stdio transports, bounded tool discovery, and MCP call metrics.
- Durable observability events are written for model, tool, MCP, and processor call lifecycle transitions.
- Model usage is persisted in model-call summaries and exposed through token counters when providers report usage.
- Agent instance version `tool_config`, `mcp_config`, `workflow_config`, and `policy_config` are validated and applied during run execution, including model-loop and per-turn tool-call limits.
- Artifact processors include a generic HTTP processor adapter for OCR/transcription/document extraction services.
- Internal model-visible control tools: `duraclaw.run_workflow` and `duraclaw.ask_user`.
- Optional admin bearer-token protection.
- Graceful SIGINT/SIGTERM shutdown.
- Basic in-process counters exposed on `/metrics`, including HTTP status counters from access logging.
