# Duraclaw

Duraclaw is an ACP-native durable agent runtime. [Nexus](https://github.com/zean00/nexus) owns channel adapters and calls Duraclaw over HTTP. The core agent loop is copied and adapted from PicoClaw, with Duraclaw adding ACP-native durability, PostgreSQL persistence, workflows, policy, scheduling, outbound delivery, and multi-tenant runtime controls.

## Documentation

Full documentation is in `docs/` and can be generated with MkDocs:

```bash
pip install mkdocs
mkdocs serve
```

The MkDocs entrypoint is `mkdocs.yml`.

Fast paths:

- [Documentation home](docs/index.md)
- [Installation](docs/installation.md)
- [Configuration](docs/configuration.md)
- [Architecture overview](docs/architecture-overview.md)
- [ACP and Admin API](docs/interfaces/api.md)
- [Durable runs](docs/concepts/durable-runs.md)
- [Agent profiles, scope, and recommendations](docs/concepts/agent-profiles.md)
- [Reminders, scheduler jobs, and background runs](docs/concepts/reminders-jobs.md)
- [Workflows](docs/concepts/workflows.md)
- [Memory, preferences, and knowledge](docs/concepts/memory-preferences-knowledge.md)

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
`/readyz` verifies database connectivity and returns queue counts for queued/active runs, pending outbox rows, queued async writes, and due scheduler jobs.

Provider configuration defaults to the built-in mock provider. Duraclaw supports OpenAI, OpenRouter, and generic OpenAI-compatible `/chat/completions` endpoints for local LLM servers.

OpenAI:

```bash
DURACLAW_PROVIDER=openai
DURACLAW_PROVIDER_API_KEY=...
DURACLAW_PROVIDER_MODEL=gpt-4.1-mini
DURACLAW_PROVIDER_FALLBACKS=mock/duraclaw
```

OpenRouter:

```bash
DURACLAW_PROVIDER=openrouter
DURACLAW_PROVIDER_API_KEY=...
DURACLAW_PROVIDER_MODEL=openai/gpt-4.1-mini
DURACLAW_PROVIDER_REFERER=https://your-app.example
DURACLAW_PROVIDER_TITLE=Duraclaw
DURACLAW_PROVIDER_FALLBACKS=mock/duraclaw
```

Generic OpenAI-compatible or local LLM:

```bash
DURACLAW_PROVIDER=openai-compatible
DURACLAW_PROVIDER_BASE_URL=http://localhost:11434/v1
DURACLAW_PROVIDER_API_KEY=...
DURACLAW_PROVIDER_MODEL=llama3.1
DURACLAW_PROVIDER_FALLBACKS=mock/duraclaw
```

OpenAI and OpenRouter chat requests support provider message content arrays for multimodal inputs. ACP run parts can include `text`, `image_url`, `file`, `input_audio`, and `video_url`; Duraclaw maps those to the OpenAI-compatible content part shapes used by both providers. Use URL/data URI fields for images/files/videos and base64 `data` plus `format` for audio.

Knowledge ingestion and workflow retrieval default to a deterministic local hash embedder. To use an OpenAI-compatible `/embeddings` endpoint:

```bash
DURACLAW_EMBEDDING_PROVIDER=openai-compatible
DURACLAW_EMBEDDING_BASE_URL=https://api.openai.com/v1
DURACLAW_EMBEDDING_API_KEY=...
DURACLAW_EMBEDDING_MODEL=text-embedding-3-small
DURACLAW_EMBEDDING_DIMENSIONS=768
```

OpenRouter embeddings are also supported:

```bash
DURACLAW_EMBEDDING_PROVIDER=openrouter
DURACLAW_EMBEDDING_API_KEY=...
DURACLAW_EMBEDDING_MODEL=openai/text-embedding-3-small
```

Outbox delivery defaults to a placeholder log sink. To push queued outbound intents to Nexus:

```bash
DURACLAW_OUTBOX_SINK=nexus
NEXUS_OUTBOUND_URL=http://nexus.internal/acp/outbound
NEXUS_TOKEN=...
```

`/readyz` reports `outbox_pending`, `outbox_unclaimed`, `outbox_claimed`, and `outbox_stale`. If local validation creates outbound rows but Nexus does not receive them, check these fields and the `outbox delivery failed` logs to confirm the outbox worker is running with the Nexus sink and not waiting on a claim lease.

Artifact processing defaults to the built-in mock processor. To use an HTTP processor for transcription, OCR, document extraction, or other media representation work:

```bash
DURACLAW_ARTIFACT_PROCESSOR_URL=http://processor.internal
DURACLAW_ARTIFACT_PROCESSOR_TOKEN=...
DURACLAW_ARTIFACT_PROCESSOR_NAME=media_processor
DURACLAW_ARTIFACT_PROCESSOR_MODALITIES=audio,image,document
DURACLAW_ARTIFACT_PROCESSOR_MEDIA_TYPES=audio/mpeg,image/png,application/pdf,text/plain
DURACLAW_ARTIFACT_PROCESSOR_TIMEOUT_SECONDS=60
DURACLAW_ARTIFACT_PROCESSOR_MAX_RESPONSE_BYTES=1048576
DURACLAW_ARTIFACT_PROCESSOR_MAX_REPRESENTATIONS=16
DURACLAW_ARTIFACT_PROCESSOR_RAW_MEDIA_ALLOWED=false
DURACLAW_ARTIFACT_PROCESSOR_MAX_RETRIES=0
```

To use a built-in provider-backed processor through OpenAI, OpenRouter, or an OpenAI-compatible/local multimodal endpoint:

```bash
DURACLAW_ARTIFACT_PROCESSOR_PROVIDER=openai
DURACLAW_ARTIFACT_PROCESSOR_API_KEY=...
DURACLAW_ARTIFACT_PROCESSOR_MODEL=gpt-4.1-mini
DURACLAW_ARTIFACT_PROCESSOR_MODALITIES=audio,image,document,video
```

For OpenRouter:

```bash
DURACLAW_ARTIFACT_PROCESSOR_PROVIDER=openrouter
DURACLAW_ARTIFACT_PROCESSOR_API_KEY=...
DURACLAW_ARTIFACT_PROCESSOR_MODEL=openai/gpt-4.1-mini
DURACLAW_ARTIFACT_PROCESSOR_MODALITIES=audio,image,document,video
```

OpenRouter audio transcription uses OpenRouter's `/audio/transcriptions` STT endpoint, defaults to `openai/whisper-large-v3` unless artifact metadata supplies `transcription_model` or `model`, and persists a `transcript` representation. Provider processors use multimodal chat input parts for other modalities and persist standard artifact representations such as `vision_summary`, `document_text`, `transcript`, and `video_summary`.

Admin endpoints are open by default for local development. Set this in shared or production environments:

```bash
DURACLAW_ADMIN_TOKEN=...
DURACLAW_ACP_TOKEN=...
DURACLAW_REQUIRE_AUTH=true
```

Optional OTLP HTTP export can push lightweight trace events and counter snapshots to an OpenTelemetry collector:

```bash
DURACLAW_OTLP_ENDPOINT=http://otel-collector:4318
DURACLAW_OTLP_HEADERS=Authorization=Bearer token
DURACLAW_OTEL_SERVICE_NAME=duraclaw
DURACLAW_OTEL_EXPORT_INTERVAL_SECONDS=10
DURACLAW_OTEL_INSECURE=true
```

Global MCP servers can be configured for all runs; agent instance version `mcp_config` can still add per-version servers:

```bash
DURACLAW_MCP_CONFIG='{"servers":[{"name":"tools","transport":"http","base_url":"http://mcp.internal"}]}'
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

The GitHub Actions workflow runs the full suite with a `pgvector/pgvector:pg17` PostgreSQL service and `TEST_DATABASE_URL` configured.

## Operations

- `/readyz` reports queue depth for queued/active runs, pending outbound rows, queued async writes, and due scheduler jobs.
- `/metrics` exposes in-process counters and duration histograms. Configure `DURACLAW_OTLP_ENDPOINT` to export OpenTelemetry SDK spans and metrics to an OTLP HTTP collector.
- Use `GET /admin/observability/events?customer_id={customer_id}` for durable debug/audit events, including MCP notifications and policy decisions.
- Use `GET /acp/runs/{run_id}/trace` with `X-Customer-ID` to inspect model, tool, MCP, processor, and run-step records for a single run.
- Failed artifact processors leave failed processor-call records and set the artifact state to `failed`; oversized processor payloads may be degraded according to configured limits.
- Outbound failures remain in `async_outbox` for retry and are visible through queue depth and outbound-intent status.
- `POST /admin/retention/run` can clean old artifacts, run events, outbox rows, async write jobs, observability events, and terminal broadcasts.

## ACP Routes

- `GET /acp/agents`
- `GET /healthz`
- `GET /readyz`
- `GET /metrics`
- `POST /admin/agent-instances/{agent_instance_id}/versions`
- `GET /admin/agent-instances/{agent_instance_id}/versions?customer_id={customer_id}`
- `POST /admin/agent-instances/{agent_instance_id}/versions/import?format=json|yaml`
- `GET /admin/agent-instances/{agent_instance_id}/versions/{version_id}/export?format=json|yaml`
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
- `PUT /admin/runtime-limits/customer/{customer_id}`
- `GET /admin/runtime-limits/customer/{customer_id}`
- `PUT /admin/runtime-limits/customer/{customer_id}/agent-instances/{agent_instance_id}`
- `GET /admin/runtime-limits/customer/{customer_id}/agent-instances/{agent_instance_id}`
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
- `GET /admin/background-runs?customer_id={customer_id}`
- `PUT /admin/runtime-limits/customer/{customer_id}`
- `GET /admin/runtime-limits/customer/{customer_id}`
- `PUT /admin/runtime-limits/customer/{customer_id}/agent-instances/{agent_instance_id}`
- `GET /admin/runtime-limits/customer/{customer_id}/agent-instances/{agent_instance_id}`
- `PUT /admin/runtime-limits/customer/{customer_id}/users/{user_id}`
- `GET /admin/runtime-limits/customer/{customer_id}/users/{user_id}`
- `GET /admin/usage/model?customer_id={customer_id}&period=daily|weekly|monthly`
- `POST /admin/sessions/{session_id}/compact`
- `POST /admin/recommendations/items`
- `GET /admin/recommendations/items?customer_id={customer_id}`
- `PATCH /admin/recommendations/items/{item_id}`
- `DELETE /admin/recommendations/items/{item_id}?customer_id={customer_id}`
- `GET /admin/recommendations/decisions?customer_id={customer_id}`
- `GET /admin/recommendations/jobs?customer_id={customer_id}`
- `PUT /admin/users/{user_id}/recommendation-delivery`
- `GET /admin/mcp/servers`
- `GET /admin/mcp/servers/{server_name}/tools`
- `GET /admin/mcp/servers/{server_name}/resources`
- `GET /admin/mcp/servers/{server_name}/resources/read?uri={uri}`
- `GET /admin/mcp/servers/{server_name}/prompts`
- `POST /admin/mcp/servers/{server_name}/prompts/{prompt_name}/get`
- `POST /admin/mcp/notifications`
- `PUT /admin/mcp/tool-access/customers/{customer_id}/agent-instances/{agent_instance_id}/servers/{server_name}`
- `GET /admin/mcp/tool-access/customers/{customer_id}/agent-instances/{agent_instance_id}/servers/{server_name}`
- `DELETE /admin/mcp/tool-access/customers/{customer_id}/agent-instances/{agent_instance_id}/servers/{server_name}`
- `PUT /admin/mcp/tool-access/customers/{customer_id}/agent-instances/{agent_instance_id}/users/{user_id}/servers/{server_name}`
- `GET /admin/mcp/tool-access/customers/{customer_id}/agent-instances/{agent_instance_id}/users/{user_id}/servers/{server_name}`
- `DELETE /admin/mcp/tool-access/customers/{customer_id}/agent-instances/{agent_instance_id}/users/{user_id}/servers/{server_name}`
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
- `GET /acp/runs/{run_id}/background-status`
- `GET /acp/runs/{run_id}/events`
- `POST /acp/runs/{run_id}/resume`
- `POST /acp/runs/{run_id}/cancel`
- `POST /acp/outbound-intents/{intent_id}/status`
- `POST /acp/reminders`
- `GET /acp/reminders?customer_id={customer_id}&user_id={user_id}`
- `PATCH /acp/reminders/{subscription_id}`
- `DELETE /acp/reminders/{subscription_id}?customer_id={customer_id}&user_id={user_id}`
- `POST /acp/scheduler/jobs`
- `GET /acp/scheduler/jobs?customer_id={customer_id}&user_id={user_id}`
- `PATCH /acp/scheduler/jobs/{job_id}`
- `DELETE /acp/scheduler/jobs/{job_id}?customer_id={customer_id}&user_id={user_id}`
- `POST /acp/shared-scheduler/subscriptions`
- `GET /acp/shared-scheduler/subscriptions?customer_id={customer_id}&user_id={user_id}`
- `PATCH /acp/shared-scheduler/subscriptions/{subscription_id}`
- `DELETE /acp/shared-scheduler/subscriptions/{subscription_id}?customer_id={customer_id}&user_id={user_id}`
- `GET /acp/background-runs?customer_id={customer_id}&user_id={user_id}`
- `POST /acp/background-runs/{run_id}/cancel`
- `GET /acp/sessions/{session_id}/runs/latest`
- `GET /acp/sessions/{session_id}/runs/by-idempotency-key/{key}`

Write requests require customer execution context headers. Existing-run write requests also require `X-Run-ID` matching the route run id.
`PUT /acp/sessions/{session_id}` can optionally enqueue a durable greeting run with body `{"send_greeting":true,"greeting_channels":["webchat"],"nickname":"Sahal"}`. When `greeting_channels` is set, Duraclaw only sends the proactive greeting for matching `X-Channel-Type` values. Greeting runs are system-initiated and do not store the internal greeting instruction as a user message. The same endpoint accepts `{"recommendation":{"blocked_channels":["whatsapp"]}}` to suppress recommendation and broadcast/promotion delivery for matching session channels while leaving webchat/default channels enabled.
Run status, event, trace, artifact, and representation reads require `X-Customer-ID` for tenant scoping.
Outbound intent status callbacks also require `X-Customer-ID` and accept `sent_to_nexus`, `delivered`, `failed`, or `cancelled` (`sent` is accepted as a compatibility alias for `sent_to_nexus`).
Retention cleanup accepts `artifact_days`, `event_days`, `outbox_days`, `async_write_days`, `observability_days`, and `broadcast_days`.
Run input may include `text` and a `parts` array. Supported part types are `text`, `artifact_ref`, `location`, and `structured_data`; `artifact_ref` parts must include `data.artifact_id`.
Run input may also include `workflow_id` or `workflow_definition_id` to execute an assigned workflow graph before the normal agent loop.

## User-Scoped Reminders And Jobs

Duraclaw exposes both admin-scoped and ACP user-scoped APIs for reminders, cron/one-time scheduler jobs, and long-running background runs. ACP routes enforce `X-Customer-ID` and `X-User-ID` against the requested `customer_id` and `user_id`; mismatches return not found.

Reminder subscriptions are durable cron-like subscriptions. Use `schedule` with a cron expression or `@once`; when `next_run_at` is omitted, Duraclaw computes the next fire time from `schedule`.

- `POST /acp/reminders` creates a user reminder subscription. Body fields: `customer_id`, `user_id`, `session_id`, `agent_instance_id`, `title`, `schedule`, `timezone`, `payload`, optional `next_run_at`, and `metadata`.
- `GET /acp/reminders?customer_id={customer_id}&user_id={user_id}&limit=100` lists that user's reminders.
- `PATCH /acp/reminders/{subscription_id}` updates user-owned reminder fields: `title`, `schedule`, `timezone`, `payload`, `next_run_at`, `metadata`, and `enabled`.
- `DELETE /acp/reminders/{subscription_id}?customer_id={customer_id}&user_id={user_id}` deletes a user-owned reminder.

Scheduler jobs are lower-level durable run triggers for one-time or recurring work, including research/background jobs. The scheduler creates runs from the stored `input` when the job fires.

- `POST /acp/scheduler/jobs` creates a user job. Body fields: `customer_id`, `user_id`, `agent_instance_id`, `session_id`, `job_type`, `schedule`, optional `next_run_at`, `input`, and `metadata`.
- `GET /acp/scheduler/jobs?customer_id={customer_id}&user_id={user_id}&limit=100` lists that user's jobs.
- `PATCH /acp/scheduler/jobs/{job_id}` updates user-owned job fields: `schedule`, `next_run_at`, `input`, `metadata`, and `enabled`.
- `DELETE /acp/scheduler/jobs/{job_id}?customer_id={customer_id}&user_id={user_id}` deletes a user-owned job.

Shared scheduler jobs are customer-level polling jobs with active user subscriptions. They support external eligibility APIs for cases such as location-specific prayer reminders. External calls omit subscriber records unless `include_subscribers` is explicitly true. The external response mapping can read eligible `user_id`s from paths such as `users`, `accounts`, or `result.users`; missing user-list paths fall back to all subscribers, while empty mapped lists select no recipients. Fanout can create outbound intents per subscriber or durable runs per selected agent instance; completed durable-run results are then pushed to subscriber outboxes by the shared scheduler.

- Admin: `POST /admin/shared-scheduler/jobs`, `GET /admin/shared-scheduler/jobs`, `PATCH /admin/shared-scheduler/jobs/{job_id}`, `DELETE /admin/shared-scheduler/jobs/{job_id}`.
- User subscriptions: `POST /acp/shared-scheduler/subscriptions`, `GET /acp/shared-scheduler/subscriptions`, `PATCH /acp/shared-scheduler/subscriptions/{subscription_id}`, `DELETE /acp/shared-scheduler/subscriptions/{subscription_id}`.

Background runs are durable runs created for long-running or asynchronous work. User-scoped management supports listing and cancellation:

- `GET /acp/background-runs?customer_id={customer_id}&user_id={user_id}&agent_instance_id={agent_instance_id}&limit=100`
- `POST /acp/background-runs/{run_id}/cancel` with body `{"customer_id":"...","user_id":"..."}`

Admin routes provide customer-wide management for the same primitives:

- `POST /admin/reminders/subscriptions`, `GET /admin/reminders/subscriptions`, `PATCH /admin/reminders/subscriptions/{subscription_id}`
- `POST /admin/scheduler/jobs`, `GET /admin/scheduler/jobs`, `PATCH /admin/scheduler/jobs/{job_id}`
- `POST /admin/shared-scheduler/jobs`, `GET /admin/shared-scheduler/jobs`, `PATCH /admin/shared-scheduler/jobs/{job_id}`, `DELETE /admin/shared-scheduler/jobs/{job_id}`
- `GET /admin/background-runs`

Workflow graph execution v1 is a durable DAG runner. It persists node states and edge activations, runs dependency-ready nodes concurrently with a default limit of 4, treats merge nodes as `all_active` joins, and supports retry/timeout policies on nodes.

Supported node types:

- `start`, `checkpoint`, `message`, and `end`: complete immediately and persist node output.
- `split`: activates all matching outgoing edges.
- `merge`: waits until all active upstream parents succeed.
- `switch`: copies a configured key into `route`.
- `condition`: routes with deterministic `equals` or `not_equals` config.
- `llm_condition`: persists a model call and requires JSON output containing `route`.
- `tool` and `tool_call`: execute a local durable tool and persist tool intent/result.
- `mcp` and `mcp_call`: execute an MCP tool and persist MCP intent/result.
- `mcp_list_resources`, `mcp_read_resource`, `mcp_subscribe_resource`, and `mcp_unsubscribe_resource`: list, read, subscribe, or unsubscribe MCP resources and persist MCP intent/result summaries.
- `mcp_list_prompts` and `mcp_get_prompt`: list or fetch MCP prompt templates and persist MCP intent/result summaries.
- `model_call`: executes a provider model call and can require strict JSON output.
- `retrieve_knowledge`: reads matching customer knowledge chunks with text search and hybrid vector/text retrieval when embeddings are available.
- `read_memory` and `write_memory`: read or persist stable user facts; writes pass policy enforcement.
- `read_preference` and `write_preference`: read or persist conditional preferences with condition metadata.
- `read_artifact`, `process_artifact`, and `write_artifact`: operate on durable artifact metadata and representations with artifact read/process policy enforcement.
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
- Broadcast creation creates per-target outbound intents for Nexus-owned delivery. `POST /admin/broadcasts` can include optional `external_broadcast_id`; Duraclaw returns the internal `broadcast_id`, reports `suppressed_targets`, and attaches a `broadcast_reference` artifact to outbound broadcast payloads. `generation.mode: "agent_per_instance"` or `"per_user"` plus `generation.agent_instance_id`, `guidelines`, `context`, and `details` lets Duraclaw generate promotion/offer/feature copy through that agent profile before fanout.
- Recent session message history used by the worker when composing model context.
- Durable session summaries and text-matched customer knowledge are included in model context.
- Latest session transfer note included in model context after reassignment.
- Context compaction for bounded prompt history.
- Artifact attachment/read/process policy checks for size, allowed media types, raw payload metadata fields, and representation reuse in prompt context.
- Memory tools: `remember` and `list_memories` for stable facts; `remember` returns a `memory_reference` artifact with the memory ID and admin update/delete API references.
- Preference tools: `save_preference` and `list_preferences` for conditional preferences; `save_preference` returns a `preference_reference` artifact with the preference ID and admin update/delete API references.
- Reminder tool: `create_reminder` creates a user reminder and returns a `reminder_reference` artifact containing the subscription ID and pause/resume/delete API references.
- Non-retryable tool metadata for write-like tools, with recovery query helpers.
- Admin text knowledge ingestion into deterministic chunks.
- Optional embedding provider seam for customer and shared knowledge chunk embeddings, with deterministic hash embeddings for tests/local use.
- Artifact processor HTTP contract for OCR/transcription/document extraction adapters, with processor-call context, trace propagation, response limits, degradation, and raw-payload validation.
- Retention maintenance hooks for old events, completed outbox rows, and artifact expiry.
- Retention maintenance hooks for observability events and terminal broadcasts.
- Lease extension and cancellation checks during worker execution.
- Retry release/backoff for failed async outbox delivery.
- Provider registry and model fallback attempts with persisted model-call records per attempt.
- Artifact processor registry for modality-specific processor selection.
- MCP manager registration now tracks transport metadata, opt-in retry attempts, per-server status, max-concurrency limits, HTTP/stdio transports, bounded tool/resource/prompt discovery, resource subscribe/unsubscribe calls, admin status/discovery routes, and MCP call metrics.
- Durable observability events are written for model, tool, MCP, processor, and MCP notification lifecycle transitions.
- Model usage is persisted in model-call summaries and exposed through token counters when providers report usage.
- Agent instance version `tool_config`, `mcp_config`, `workflow_config`, and `policy_config` are validated and applied during run execution, including model-loop shortlisting, tool aliases, and per-turn tool-call limits.
- Database-managed runtime limits hard-fail run, workflow, background-job, model-token, and model-cost usage when configured quotas are exceeded. Model token/cost quotas can be scoped to customer, agent instance, or user, and usage summaries are exposed through admin APIs.
- Async write jobs provide a bounded non-critical sidecar pipeline with degrade/drop metrics for oversized debug and observability payloads, including high-volume run events such as streaming model deltas and activity status telemetry.
- Checkpoints carry trace metadata when Nexus supplies `traceparent` or `X-Trace-ID`; background runs expose status APIs and progress storage.
- MCP supports SSE request negotiation, global `DURACLAW_MCP_CONFIG`, notification ingestion, and opt-in long-lived stdio JSON-RPC clients.
- Policy conditions support composite `all`/`any`/`not`, membership, prefix/suffix, and regex matching.
- Artifact processors include a generic HTTP processor adapter for OCR/transcription/document extraction services.
- Internal model-visible control tools: `duraclaw.run_workflow` and `duraclaw.ask_user`.
- Optional admin bearer-token protection.
- Graceful SIGINT/SIGTERM shutdown.
- Basic in-process counters exposed on `/metrics`, including HTTP status counters from access logging, plus OpenTelemetry SDK spans/metrics export when configured.
