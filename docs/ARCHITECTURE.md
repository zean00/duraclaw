# Duraclaw Architecture

Duraclaw is an ACP-native personal assistant runtime designed to run durable, resumable agent work for many customers. It does not connect directly to chat channels. Nexus owns channel integrations and calls Duraclaw through ACP over HTTP.

The core design goal is to make customer context, memory, tools, MCP calls, long-running work, reminders, and broadcast messages durable and auditable without requiring an external workflow engine such as Temporal.

## System Boundary

```text
Channel adapters
  -> Nexus
    -> ACP over HTTP
      -> Duraclaw
        -> LLM providers
        -> MCP servers
        -> local tools
        -> PostgreSQL + pgvector
```

Nexus is responsible for:

- Channel adapters and channel-specific delivery.
- Customer/user/channel identity resolution.
- Routing inbound messages to the correct Duraclaw agent instance and session.
- Push delivery for reminders, broadcasts, alerts, and agent-generated outbound messages.
- Channel retry, delivery status, trust UI, and user-facing approval/clarification surfaces.

Duraclaw is responsible for:

- ACP-native agent runtime APIs.
- Agent instance and session management.
- Durable run execution.
- Agent loop orchestration.
- Controlled workflow orchestration.
- Prompt and context building.
- Shared knowledge, customer knowledge, preferences, and memory.
- Tool and MCP execution.
- Customer context propagation to tools and MCP servers.
- Cron scheduling, subscriber-based reminders, background jobs, and broadcast intent creation.

Duraclaw must not contain Telegram, WhatsApp, email, webchat, or other direct channel integrations.

## ACP Native Interface

Duraclaw exposes ACP over HTTP as its primary protocol. Nexus is the expected ACP client.

Required ACP capabilities:

- Discover agent manifests.
- Ensure or reload a session.
- Start a run.
- Attach durable input artifacts to a run.
- Stream run events.
- Resume a waiting run.
- Get run status.
- Cancel a run.
- Find run by idempotency key.
- Find latest run for a session.

Admin configuration APIs also manage agent instance versions. A run must snapshot the active `agent_instance_version_id` when it is created so in-flight work remains tied to the configuration it started with.

Duraclaw should implement these concepts even if the concrete HTTP routes are adapted to Nexus' strict/native ACP client.

```text
GET    /acp/agents
PUT    /acp/sessions/{session_id}
POST   /acp/sessions/{session_id}/reassign
POST   /acp/runs
POST   /acp/runs/{run_id}/artifacts
GET    /acp/runs/{run_id}
GET    /acp/runs/{run_id}/events
POST   /acp/runs/{run_id}/resume
POST   /acp/runs/{run_id}/cancel
GET    /acp/sessions/{session_id}/runs/latest
GET    /acp/sessions/{session_id}/runs/by-idempotency-key/{key}
```

All ACP requests from Nexus must include customer execution context in headers.

Required headers:

```text
X-Customer-ID
X-User-ID
X-Agent-Instance-ID
X-Session-ID
X-Run-ID
X-Request-ID
X-Idempotency-Key
```

Optional headers:

```text
X-Channel-Type
X-Channel-User-ID
X-Channel-Conversation-ID
X-Trace-ID
```

Duraclaw should persist the received context on the run and propagate it to all downstream tool and MCP executions.

ACP run creation and resume payloads may include structured content parts, not only text. Nexus is responsible for normalizing channel-specific media into a channel-neutral artifact envelope before calling Duraclaw.

Content part examples:

```text
text
artifact_ref
location
structured_data
```

Artifact references should identify media by durable storage reference, signed URL, or object key plus metadata. Duraclaw should not depend on WhatsApp, Telegram, email, or browser-specific media URLs.

Artifact reference fields:

```text
artifact_id
modality
media_type
filename
size_bytes
checksum
storage_ref
source_channel
source_message_id
metadata
```

## Modality and Artifact Support

Duraclaw treats images, audio, documents, video, and future media types as durable artifacts. Artifacts are first-class run inputs and outputs, but channel transport remains outside Duraclaw.

Initial required modalities:

- `audio`: voice notes and other audio messages.
- `image`: photos, screenshots, scans, receipts, posters, and handwritten notes.
- `document`: PDFs, office documents, text files, and uploaded attachments.

Future modalities should fit the same model:

- `video`
- `location`
- `contact`
- `calendar_event`
- custom customer-defined structured artifacts

Artifact lifecycle:

```text
Nexus receives channel-specific media
  -> Nexus stores media or creates a durable storage reference
  -> Nexus sends ACP run/resume with artifact metadata
  -> Duraclaw persists artifact records
  -> Duraclaw selects allowed processors by modality, media type, policy, and agent instance
  -> processors create artifact representations
  -> prompt/context builder includes relevant representations
  -> normal agent loop or workflow continues
```

Artifact states:

```text
pending
available
processing
processed
failed
expired
deleted
```

Artifact representations are derived views of an artifact. The core agent loop should consume representations rather than raw binary payloads whenever possible.

Representation examples:

```text
transcript
ocr_text
vision_summary
document_text
document_outline
structured_fields
thumbnail
embedding
moderation_result
metadata_summary
```

Processors are tools or MCP calls with artifact-aware contracts. They should be selected explicitly by the runtime, workflow, or policy, not hidden inside ad hoc prompt behavior.

Processor examples:

- Audio transcription.
- Audio language detection.
- Image OCR.
- Image understanding.
- Document text extraction.
- Document summarization.
- File type detection.
- Thumbnail generation.
- Video keyframe extraction.
- Video transcription.
- Structured data extraction.

Artifact processing requirements:

- Persist artifact metadata before processing.
- Persist processor intent before accessing or transforming an artifact.
- Persist representation metadata and summaries after processing.
- Treat raw binary payload snapshots as sensitive and avoid storing them in logs.
- Enforce customer isolation on artifact storage and retrieval.
- Enforce file size, media type, retention, and processor permissions by policy.
- Avoid sending raw media to a model provider unless policy and processor configuration allow it.
- Record enough processor output to resume the run without repeating non-idempotent or expensive processing when possible.
- Support degraded processing when a full representation is too large, for example summary-only document extraction.

Artifacts may be inbound user inputs, generated outputs, workflow intermediates, or admin-managed knowledge sources. Artifact records should not encode product-specific channel behavior.

## Customer, Agent Instance, and Session Model

Duraclaw uses `customer_id` as the top-level tenant boundary.

```text
customer
  -> agent_instance
    -> session
      -> run
        -> step
```

Agent instances are admin-managed. A customer/user cannot create arbitrary agent instances through normal assistant usage.

An agent instance owns:

- Model configuration.
- Provider fallback policy.
- System instructions, behavior guidelines, and policy packs.
- Agent profile configuration: personality, communication style, language capability, domain scope, and out-of-scope response guidance.
- Enabled workflows.
- Enabled local tools.
- Enabled MCP servers.
- Knowledge access policy.
- Memory policy.
- Cron and background-job permissions.

Agent instance versions are immutable configuration snapshots. Activating a version updates the agent instance's current pointer; existing runs keep their original `agent_instance_version_id`. Version snapshots can provide system instructions, profile configuration, policy-pack pins, and model fallback configuration for agent-loop and workflow model calls.

Agent profile configuration is distinct from policy packs. Profiles describe how the agent should present itself and what domain it is meant to cover. Policy packs remain the reusable enforcement and audit mechanism. A profile can include:

- Personality and communication style.
- Supported languages or language behavior.
- Allowed and forbidden domains.
- Guidance for out-of-scope responses.
- Optional scope-judge model and confidence threshold.

Before the normal assistant model, tools, workflows, or MCP calls run, Duraclaw may call a scope judge model using the snapshotted profile configuration. If the judge determines the request is out of scope, the run completes with the configured out-of-scope response and no side-effecting tool/workflow/MCP execution occurs.

A session belongs to one user and is shared across channels. The same user on WhatsApp, webchat, or another channel should map to the same Duraclaw session when Nexus resolves them as the same user.

```text
same customer_id + same user_id = same assistant session
```

The current agent instance assignment is mutable. This allows a user to move between regular and premium agent instances when their service level changes.

Session reassignment rules:

- A session has a current `agent_instance_id`.
- Reassignment creates a durable `session_agent_instance_transfers` record.
- Session ensure/reload and run creation must not silently change `agent_instance_id`; reassignment uses the explicit transfer path.
- Run creation snapshots the persisted session agent and that agent's active version, not a divergent request header.
- Conversation history and memory remain attached to the session.
- Future runs use the new agent instance.
- Existing in-flight runs continue on the agent instance they started with unless explicitly cancelled and restarted.
- Prompt/context building should include a short transfer note when the agent instance changes materially.

Long-lived sessions are monitored by a background session monitor. The monitor claims idle sessions using PostgreSQL leases, derives compact prompt context from full message history, extracts stable memories and conditional preferences, and updates active-time patterns. It does not send user-facing ads, promotions, or suggestions in v1; it only records active-pattern data for later broadcast or suggestion targeting.

## Durable Run Execution

Every user turn, cron trigger, background job, reminder, and broadcast-generation task is represented as a durable run.

Duraclaw uses PostgreSQL as the durability engine. No external workflow engine is required for the initial architecture.

Run states:

```text
queued
leased
running
running_workflow
awaiting_user
completed
failed
cancelled
expired
```

Step states:

```text
pending
running
succeeded
failed
skipped
cancelled
```

Durable execution requirements:

- Persist run before starting execution.
- Persist every model call request/response summary.
- Persist every tool call request/response summary.
- Persist ACP stream events.
- Persist checkpoints after each meaningful step.
- Emit structured logs, metrics, and trace spans at each checkpoint.
- Keep the critical checkpoint path small and synchronous.
- Write heavy checkpoint details, debug payloads, and observability records asynchronously.
- Use leases so another worker can recover abandoned runs.
- Use idempotency keys for inbound user messages and scheduled jobs.
- Resume from the latest committed checkpoint after worker crash.
- Do not repeat non-idempotent tool calls unless the tool call is explicitly marked retryable.

Recommended execution loop:

```text
load runnable run
acquire lease
load agent instance
load session
load input artifacts and available representations
build context
call model
if artifacts require processing before context is usable:
  persist artifact_processor_call
  execute processor with customer context
  persist artifact_representation
  checkpoint
  continue model loop
if model requests tools:
  persist tool_call
  execute tool with customer context
  persist tool_result
  checkpoint
  continue model loop
if model selects workflow:
  persist workflow_run
  switch run to workflow mode
  execute workflow until complete or awaiting_user
  return workflow result to agent run
if model asks for clarification:
  persist awaiting_user
  emit ACP await event
if final response:
  persist assistant message
  emit ACP completed event
  complete run
release lease
```

Crash recovery is based on persisted run, step, event, tool-call, and checkpoint records. In-memory state is treated as disposable.

Durability writes use a two-tier model:

```text
critical durability writes
  -> synchronous, required for correctness

non-critical durability details
  -> asynchronous, best effort or delayed
```

Critical synchronous writes:

- Run creation.
- Run status transitions.
- Lease acquisition and lease extension.
- Idempotency records.
- Step status transitions.
- Artifact metadata before processing.
- Artifact processor intent before execution.
- Artifact representation metadata needed for recovery.
- Tool call intent before execution.
- Non-idempotent tool result after execution.
- Awaiting-user state.
- Final assistant message pointer.
- Minimal checkpoint cursor needed for recovery.

Asynchronous best-effort writes:

- Full model request/response payload snapshots.
- Full tool payload snapshots.
- Full MCP payload snapshots.
- Full artifact processor payload snapshots.
- Full artifact representation payload snapshots.
- Verbose debug records.
- Derived observability events.
- Non-critical ACP stream event copies.
- Token accounting details if provider response was already committed elsewhere.

This keeps the hot path small while preserving actual resumability. If an asynchronous write is lost, Duraclaw may lose some debug detail, but it must not lose the ability to determine whether a run, step, or non-idempotent tool call already happened.

Asynchronous writes should use a bounded in-process buffer plus a PostgreSQL-backed outbox for records that must eventually be flushed but are not required before the next agent step.

Backpressure behavior:

- Critical durability writes block the run.
- Async debug/observability writes may be dropped after limits are exceeded.
- Async payload snapshots may be degraded to summaries.
- The system should increment drop/degrade metrics when this happens.
- Operators should be able to configure per-customer or global async buffer limits.

## Agent Loop

Duraclaw should reuse the agent loop concepts from PicoClaw, but not its in-memory turn lifecycle as-is.

Reuse/adapt from PicoClaw:

- Provider abstraction.
- MCP manager and tool wrapper ideas.
- Agent instance/session concepts.
- Prompt/context building patterns.
- Tool execution loop concepts.
- Hook points for policy and context enrichment.

Replace or redesign:

- In-memory active turn state.
- JSONL as primary persistence.
- Channel-aware gateway execution.
- Local filesystem workspace as tenant boundary.
- Cron implementation that assumes one local runtime.

Duraclaw's agent loop should be durable-run driven:

```text
run worker
  -> durable run pipeline
    -> agent instance config
    -> session state
    -> prompt/context builder
    -> provider call
    -> tool/MCP executor
    -> event/checkpoint persistence
```

Same-session runs should be serialized by default. Different sessions may run concurrently, limited by worker capacity and customer/instance quotas.

## Workflow Orchestration

Duraclaw should not depend on uncontrolled skill packages as the primary way to automate repeated work. Instead, it should support admin-defined workflows: explicit, versioned orchestration graphs with durable state, policy controls, and observable execution.

Workflows are similar in purpose to agent skills because they describe reusable tasks and when they are useful. The difference is that workflows are structured, permissioned, versioned, auditable, and executed by a workflow runtime instead of being free-form prompt instructions.

A workflow defines:

- What task it performs.
- When the agent may use it.
- Required inputs.
- Allowed callers.
- Allowed tools and MCP servers.
- Step graph.
- User clarification points.
- Retry and timeout policy.
- Output contract.
- Policy constraints.

Workflow records:

```text
workflow_definition
  id
  name
  version
  status
  description
  when_to_use
  input_schema
  output_schema
  owner_scope

workflow_node
  workflow_definition_id
  node_key
  node_type
  config
  retry_policy
  timeout_policy

workflow_edge
  workflow_definition_id
  from_node_key
  to_node_key
  condition

workflow_assignment
  workflow_definition_id
  customer_id
  agent_instance_id
  enabled
```

Workflow node types:

```text
start
end
tool_call
mcp_call
model_call
retrieve_knowledge
read_memory
write_memory
read_artifact
process_artifact
write_artifact
ask_user
branch
loop
transform
wait_timer
emit_outbound_message
create_background_job
```

Workflow execution records:

```text
workflow_run
  id
  run_id
  workflow_definition_id
  workflow_version
  status
  current_node_key
  input
  output
  started_at
  completed_at

workflow_node_run
  workflow_run_id
  node_key
  status
  attempts
  input_ref
  output_ref
  error
  started_at
  completed_at
```

Workflow run states:

```text
queued
running
awaiting_user
succeeded
failed
cancelled
expired
```

Workflow mode:

- The agent may choose a workflow only if policy and agent instance configuration allow it.
- When a workflow is selected, the parent run enters `running_workflow`.
- The normal iterative tool-calling loop pauses.
- The workflow runtime owns execution until the workflow succeeds, fails, is cancelled, or waits for user input.
- The agent exits workflow mode only after the workflow reaches a terminal state or returns control with a structured result.
- Workflow node execution uses the same customer context propagation as normal tool/MCP execution.
- Workflow state is checkpointed independently from the parent agent run.

Workflow selection should be policy-aware. The prompt/context builder may include workflow manifests, but runtime policy must still enforce whether the selected workflow is allowed.

Workflow manifests exposed to the model should be concise:

```text
workflow_id
name
description
when_to_use
required_inputs
expected_output
```

The model should not receive the full internal graph unless required. The graph is executed by Duraclaw, not improvised by the model.

Workflow clarification flow:

```text
workflow node asks user
  -> persist workflow_run awaiting_user
  -> persist parent run awaiting_user
  -> emit ACP await event through Nexus
  -> user replies
  -> Nexus calls ACP resume
  -> Duraclaw resumes workflow node
```

Workflow durability:

- Workflow definition version is pinned when a workflow run starts.
- Every node transition is persisted.
- Tool/MCP node intent is persisted before execution.
- Non-idempotent node result is persisted before moving to the next node.
- Heavy node payloads may use the same async best-effort write path as run checkpoints.
- Recovery resumes from the latest completed node or retryable in-progress node.

Workflow examples:

- Research a topic and produce a sourced brief.
- Scrape a configured website and summarize changes.
- Ask user for missing reminder details, then schedule a cron subscription.
- Collect preferences and update user memory.
- Process a voice note, image, or document, then continue the user request with extracted context.
- Generate a broadcast draft, segment recipients, and emit outbound messages after policy checks.

Workflows are not a replacement for tools. Tools are single capabilities. Workflows are durable orchestrations that may call tools, MCP servers, model steps, retrieval, memory, and user clarification nodes.

## Native Customer Context Propagation

Customer context is first-class execution state, not optional metadata.

Execution context:

```text
customer_id
user_id
agent_instance_id
session_id
run_id
step_id
tool_call_id
artifact_id
artifact_processor_call_id
channel_type
channel_user_id
channel_conversation_id
request_id
idempotency_key
permissions
```

Local tools receive this context through the internal tool execution API.

MCP servers receive this context through HTTP headers. For stdio MCP servers, Duraclaw should wrap calls and inject the same context into the MCP client environment or an agreed metadata envelope if headers are not available.

Required MCP HTTP headers:

```text
X-Customer-ID
X-User-ID
X-Agent-Instance-ID
X-Session-ID
X-Run-ID
X-Tool-Call-ID
X-Request-ID
```

MCP tool arguments should remain domain-specific. Customer context should not be duplicated into tool arguments unless a specific MCP server requires it.

Implementation status: Duraclaw's MCP manager tracks registered server specs, transport type, opt-in retry settings, max-concurrency limits, last-use status, last error, call/failure counts, bounded tool discovery, MCP resource list/read/subscribe/unsubscribe operations, and MCP prompt list/get operations. HTTP clients propagate Duraclaw context headers, SSE clients parse `text/event-stream` data frames, stdio clients speak MCP JSON-RPC and inject context into the command environment, opt-in long-lived stdio clients can reuse a JSON-RPC process, and MCP calls persist intent/result records before and after execution. Global `DURACLAW_MCP_CONFIG` and agent instance version `mcp_config` can register MCP servers, admin MCP routes expose server status and discovery output, and an admin notification endpoint persists MCP resource-change notifications as durable observability events.

## Knowledge, Preferences, and Memory

PostgreSQL is the source of truth for knowledge, preferences, memory, messages, run history, and vector search.

Use PostgreSQL with pgvector. Embedding dimension is 768 to support OpenAI-compatible embedding models and EmbeddingGemma-style models.

Knowledge scopes:

- Shared knowledge: available to many or all customers.
- Customer knowledge: available only within one `customer_id`.
- User metadata: flexible per-user JSON for customer-provided profile data.
- User preferences: stable preferences for one user.
- User memory: long-lived facts inferred or explicitly saved for one user.
- Session memory: conversation-scoped summaries and recent context.
- Run context: temporary facts used during a single run.

Admins may manually create, edit, disable, and delete shared knowledge, customer knowledge, preferences, and memory records.

Memory writes should be policy-controlled. Not every conversation fact should become memory. Stable facts that rarely change belong in memories. Conditional preferences, such as seasonal or time-dependent preferences, belong in preferences with structured conditions.

Full session history remains in `messages`. Prompt-fed session context is stored separately as durable session summaries and compacted context metadata. The session monitor updates this context after a session has been idle long enough or when the conversation exceeds the configured compaction threshold.

Canonical user profile data is not modeled as first-class columns. Deployments may configure a customer profile retriever that loads profile data from an external database, API, CRM, identity provider, or Nexus and stores the normalized result in `users.metadata.profile` with `users.metadata.profile_source`. Prompt context includes only explicitly allowlisted profile fields.

Retrieval should combine:

- Recent session messages.
- Session summary.
- User preferences.
- User memory.
- Customer knowledge.
- Shared knowledge.
- Active run state.

Implementation status: prompt context now includes agent profile instructions, recent messages, durable session summaries, stable memories, conditional preferences, text/hybrid-vector customer and shared knowledge, workflow manifests, MCP tool manifests, transfer notes, policy prompt instructions, and durable artifact representations. Knowledge chunks have full-text and pgvector index migrations, knowledge documents/chunks carry explicit `customer` or `shared` scope, workflow retrieval nodes prefer hybrid vector/text retrieval when an embedder is configured, and the idle session monitor can compact context, extract memories/preferences, and update active-time patterns.

## Database Architecture

Primary database: PostgreSQL.

Required extensions:

```sql
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pgcrypto;
```

Core tables:

```text
customers
users
agent_instances
agent_instance_versions
sessions
session_agent_instance_transfers
messages
runs
run_steps
run_events
run_checkpoints
async_write_outbox
observability_events
tool_calls
mcp_servers
mcp_tool_calls
artifacts
artifact_representations
artifact_processor_calls
workflow_definitions
workflow_nodes
workflow_edges
workflow_assignments
workflow_runs
workflow_node_runs
policy_packs
policy_rules
policy_assignments
policy_evaluations
knowledge_documents
knowledge_chunks
memories
preferences
cron_jobs
cron_subscriptions
background_jobs
broadcasts
broadcast_targets
broadcast_deliveries
outbound_messages
```

Artifact records:

```text
artifacts
  id
  customer_id
  user_id
  session_id
  run_id
  message_id
  modality
  media_type
  filename
  size_bytes
  checksum
  storage_ref
  source_channel
  source_message_id
  status
  retention_policy
  metadata
  created_at
  expires_at

artifact_representations
  id
  artifact_id
  representation_type
  media_type
  content_ref
  content_summary
  embedding
  token_count
  status
  created_by_processor_call_id
  created_at

artifact_processor_calls
  id
  artifact_id
  run_id
  step_id
  processor_type
  provider
  model
  input_ref
  output_ref
  output_summary
  status
  error
  started_at
  completed_at
```

`storage_ref`, `content_ref`, `input_ref`, and `output_ref` may point to object storage, encrypted database payload records, or another durable storage system. The database should store enough metadata for recovery, audit, and policy evaluation without requiring raw binary payloads in hot tables.

Vector-bearing tables and vector-bearing artifact representations should use `vector(768)`.

Important indexes:

```text
sessions(customer_id, user_id)
sessions(customer_id, agent_instance_id)
runs(status, lease_expires_at)
runs(customer_id, session_id, created_at)
runs(customer_id, session_id, idempotency_key)
run_events(run_id, sequence)
messages(session_id, created_at)
artifacts(customer_id, session_id, run_id, created_at)
artifacts(customer_id, modality, media_type, created_at)
artifact_representations(artifact_id, representation_type, status)
artifact_processor_calls(artifact_id, status, created_at)
knowledge_chunks(customer_id, scope, embedding vector index)
memories(customer_id, user_id, embedding vector index)
cron_jobs(next_run_at, enabled)
broadcast_targets(broadcast_id, status)
outbound_messages(status, created_at)
```

Use pgvector indexes appropriate for the installed PostgreSQL/pgvector version, likely HNSW for production.

## Cron and Scheduling

Duraclaw supports shared cron jobs and per-user cron jobs.

Shared cron jobs use a subscriber mechanism.

Example:

```text
holiday reminder cron
  -> many subscribers
    -> create one durable run per due subscriber
      -> emit outbound push intent to Nexus
```

Cron job types:

- System/shared cron.
- Agent-instance cron.
- Customer cron.
- Per-user cron.

Cron subscription fields:

```text
cron_job_id
customer_id
user_id
session_id
agent_instance_id
timezone
enabled
delivery_preferences
metadata
```

All cron executions must create durable runs using deterministic idempotency keys.

```text
cron:{cron_job_id}:{subscription_id}:{scheduled_fire_time}
```

The scheduler should be PostgreSQL-backed:

- Poll due jobs.
- Acquire row-level locks with `FOR UPDATE SKIP LOCKED`.
- Claim due reminder subscriptions with row-level locks and fan out one durable run per subscription.
- Create run records with deterministic idempotency keys.
- Advance `next_run_at`.
- Disable one-shot jobs or subscriptions after the first successful fire.
- Workers execute resulting runs.

## Background Jobs

Background jobs are durable runs with a background mode. They are intended for tasks that take minutes, such as research, scraping, summarization, enrichment, and workflow execution.

Background job requirements:

- Durable status.
- Checkpoints.
- Cancellation.
- Progress events.
- Tool/MCP context propagation.
- User-visible completion or failure notification through Nexus.

Background jobs should not require a separate execution model from normal runs. They should reuse the same durable run pipeline with different quotas and timeouts.

## Broadcast and Push Messages

Duraclaw supports broadcast creation and targeting, but Nexus owns channel delivery.

Broadcast content may be:

- Manually authored by an admin.
- Generated by an agent and approved or sent according to policy.

Broadcast flow:

```text
admin or agent creates broadcast
Duraclaw resolves targets
Duraclaw creates outbound message records
Nexus pulls or receives push intents
Nexus delivers through channels
Nexus reports delivery status back
Duraclaw records delivery status
```

Broadcast target selection should support:

- All users in a customer.
- Subscribers to a shared cron/reminder.
- Users attached to an agent instance.
- Explicit user list.
- Segment query.

Implementation status: broadcast creation accepts explicit targets and database-resolved target selections for all customer users, specific user IDs, users attached to an agent instance, reminder subscribers, and a constrained segment selector over durable session fields (`user_id_prefix`, `session_id_prefix`, `agent_instance_id`, and RFC3339 `updated_since`). Arbitrary SQL segment queries are intentionally not accepted. Nexus delivery status callbacks accept `sent_to_nexus`, `delivered`, `failed`, and `cancelled`, update outbound and broadcast target state transactionally, and persist durable outbound acknowledgement observability events. Duraclaw can push outbound intents to Nexus one at a time or in topic-grouped bulk batches when `NEXUS_OUTBOUND_BULK_URL` is configured.

Outbound message states:

```text
pending
sent_to_nexus
delivered
failed
cancelled
```

Duraclaw should expose an outbound/push API or event stream for Nexus to consume. Nexus must be modified to support system-initiated push delivery for reminders, alerts, broadcasts, and completed background jobs.

## Nexus Integration

Nexus should continue to be the channel gateway. Required Nexus changes:

- Support Duraclaw as a strict/native ACP backend over HTTP.
- Send customer/user/session/agent context headers on all ACP calls.
- Resolve one Duraclaw session per user across channels.
- Support session reassignment to a different agent instance.
- Support system-initiated push messages from Duraclaw.
- Report delivery status for outbound messages.
- Preserve idempotency keys when forwarding inbound messages and push delivery acknowledgements.

Recommended Nexus-to-Duraclaw message flow:

```text
inbound channel event
  -> Nexus canonical event
  -> resolve customer_id + user_id
  -> resolve Duraclaw session
  -> resolve current agent_instance_id
  -> store or reference channel media as artifacts
  -> ACP StartRun with context headers and content parts
  -> receive stream events
  -> deliver user-facing output through channel
```

Recommended Duraclaw-to-Nexus push flow:

```text
cron/background/broadcast completes
  -> Duraclaw creates outbound_message
  -> Nexus fetches or receives push intent
  -> Nexus delivers to best channel
  -> Nexus reports delivery status
```

## Provider and Embedding Architecture

Duraclaw should reuse PicoClaw's provider abstraction ideas for chat models and adapt them for embeddings.

Provider responsibilities:

- Chat completion.
- Streaming.
- Tool-call compatible responses.
- Fallback candidates.
- Retry policy.
- Embeddings.
- Artifact processor adapters when providers directly support modalities.

Embedding configuration:

```text
dimension: 768
provider: configurable
model: configurable
```

The provider layer should allow OpenAI-compatible providers and local/alternate embedding providers such as EmbeddingGemma-compatible deployments.

Artifact processor configuration:

```text
processor_type: transcription | ocr | vision | document_extraction | summarization | video_extraction | custom
supported_modalities: configurable
supported_media_types: configurable
provider: configurable
model: configurable
max_size_bytes: configurable
retention_policy: configurable
raw_media_allowed: configurable
```

Some processors may call multimodal model providers directly. Others may use local libraries, OCR engines, speech-to-text services, or MCP servers. The runtime should expose them through the same durable processor-call abstraction so modality support can expand without changing ACP or the agent loop.

## Security and Policy

Policy and guidelines are first-class configuration, not only prompt text. Duraclaw should support reusable policy packs that define how an agent may behave, what it may answer, when it must refuse, which tools it may use, and what tone or workflow guidelines it must follow.

Policies are admin-managed and versioned. Agent instances reference policy pack versions so behavior can be audited and changed safely.

Policy scopes:

- Global policy: applies to every agent instance.
- Customer policy: applies to one `customer_id`.
- Agent instance policy: applies to one agent instance.
- Tool policy: applies before and after local tool or MCP execution.
- Artifact policy: controls which modalities, media types, processors, and retention rules are allowed.
- Memory policy: controls what can be saved, retrieved, or forgotten.
- Broadcast policy: controls who may generate and send broadcast content.

Policy types:

- Behavioral guidelines: tone, response style, boundaries, escalation behavior.
- Domain scope: what the agent is allowed or not allowed to help with.
- Safety rules: forbidden content, required refusals, sensitive operations.
- Tool permissions: allowed tools, blocked tools, rate limits, and required context.
- Artifact permissions: allowed modalities, media types, file sizes, processors, provider routing, and retention.
- Data access rules: knowledge scopes, customer isolation, memory access.
- Output rules: required formatting, disclaimers, citation behavior, language behavior.
- Workflow rules: when to ask clarification, when to create background jobs, when to push reminders.
- Workflow permissions: which workflows may be selected, required inputs, and blocked workflow modes.

Policy records should be structured enough for enforcement and also renderable into prompt instructions.

Example policy model:

```text
policy_pack
  id
  name
  version
  status
  owner_scope

policy_rule
  policy_pack_id
  rule_type
  priority
  enforcement_mode
  condition
  action
  instruction_text

policy_assignment
  policy_pack_id
  customer_id
  agent_instance_id
  enabled
```

Enforcement modes:

```text
prompt
pre_run
pre_model
post_model
pre_tool
post_tool
pre_artifact_read
pre_artifact_process
post_artifact_process
pre_memory_write
pre_broadcast
pre_workflow
post_workflow
pre_workflow_node
post_workflow_node
```

Prompt-mode rules are compiled into the system/developer prompt. Runtime enforcement rules are evaluated in code before or after the relevant step.

Policy controls:

- System instructions.
- Allowed tools.
- Allowed MCP servers.
- Allowed artifact modalities and processors.
- Knowledge scopes.
- Memory write permissions.
- Background job permissions.
- Broadcast permissions.
- Workflow permissions.
- User clarification behavior.
- Customer data isolation.
- Agent response style and guidelines.
- Domain boundaries and out-of-scope behavior.

Policy evaluation should be recorded for auditability:

```text
run_id
step_id
policy_pack_id
policy_rule_id
artifact_id
artifact_processor_call_id
decision
reason
created_at
```

Policy decisions:

```text
allow
deny
modify
require_clarification
require_background_job
```

The prompt/context builder must merge policies in deterministic order:

```text
global policy
  -> customer policy
    -> agent instance policy
      -> run-specific context
```

When policies conflict, the stricter rule wins unless an admin-defined priority explicitly overrides it.

Duraclaw only needs user clarification waits for v1. It does not need a broader human approval system yet.

Supported await state:

```text
awaiting_user
```

Clarification flow:

```text
agent needs more information
  -> persist awaiting_user
  -> emit ACP await event
  -> Nexus asks the user
  -> user replies
  -> Nexus calls ACP resume
  -> Duraclaw continues the same run
```

## Observability

Durability checkpoints are also observability boundaries. Every important transition in the run pipeline should produce structured logs, metrics, and trace spans using the same identifiers stored in PostgreSQL.

The goal is to make any run debuggable from either direction:

- Start from a customer/user/session and find all related runs.
- Start from a failed run and inspect every model call, tool call, MCP call, checkpoint, policy decision, and outbound message.
- Start from a trace ID in logs and map it back to durable database records.

Required correlation fields:

```text
trace_id
request_id
customer_id
user_id
agent_instance_id
session_id
run_id
step_id
tool_call_id
mcp_tool_call_id
artifact_id
artifact_processor_call_id
cron_job_id
background_job_id
broadcast_id
outbound_message_id
```

Observation points:

- ACP request received.
- ACP session ensured or reloaded.
- Run created.
- Run leased.
- Run resumed from checkpoint.
- Context retrieval started/completed.
- Prompt built.
- Policy evaluated.
- Workflow selected/started/completed/failed.
- Workflow node started/completed/failed.
- Model call started/completed/failed.
- Tool call started/completed/failed.
- MCP call started/completed/failed.
- Artifact received/available/expired/deleted.
- Artifact processor call started/completed/failed.
- Artifact representation created/degraded/deleted.
- Memory read/write started/completed.
- Knowledge retrieval started/completed.
- Checkpoint written.
- Awaiting user clarification.
- Run completed/failed/cancelled/expired.
- Cron job claimed/fired.
- Background job progress updated.
- Broadcast target resolved.
- Outbound message created/sent/acknowledged/failed.

Structured logs should be JSON and include the correlation fields above. Logs should not include raw sensitive user data, full prompts, full model responses, or full tool payloads by default. Sensitive payloads should be referenced by durable record ID and inspected through authorized admin tooling.

Metrics should include:

```text
runs_created_total
runs_completed_total
runs_failed_total
runs_recovered_total
run_duration_seconds
run_queue_lag_seconds
checkpoint_write_total
checkpoint_write_failed_total
async_write_enqueued_total
async_write_flushed_total
async_write_dropped_total
async_write_degraded_total
model_call_total
model_call_duration_seconds
model_token_input_total
model_token_output_total
tool_call_total
tool_call_duration_seconds
mcp_call_total
mcp_call_duration_seconds
artifact_created_total
artifact_processor_call_total
artifact_processor_duration_seconds
artifact_processor_failed_total
artifact_representation_created_total
artifact_representation_degraded_total
workflow_run_total
workflow_run_duration_seconds
workflow_node_total
workflow_node_duration_seconds
policy_denied_total
awaiting_user_total
cron_fire_total
background_job_duration_seconds
outbound_message_total
outbound_delivery_failed_total
```

Traces should follow the durable run hierarchy:

```text
ACP StartRun
  -> durable_run
    -> context_retrieval
    -> prompt_build
    -> model_call
    -> tool_loop
      -> tool_call
      -> mcp_call
      -> artifact_processor_call
    -> workflow_run
      -> workflow_node
    -> checkpoint
    -> outbound_event
```

Checkpoints should store the active `trace_id` and last completed span name so crash recovery can continue with a new span linked to the previous trace context.

Duraclaw should support OpenTelemetry-compatible tracing and metrics. PostgreSQL remains the durable source of truth; external observability systems are optional operational integrations.

The `observability_events` table is for durable audit/debug events that are too important to exist only in logs.

Implementation status: model, tool, MCP, and artifact processor call starts/completions write both run events and durable observability events. In-process metrics expose counters plus duration count/sum/bucket series through `/metrics`, including run queue lag, run duration, model token usage, async write drop/degrade/flush counts, and model/tool/MCP/artifact processor/workflow-node durations. OpenTelemetry SDK tracing/metrics can export to OTLP HTTP collectors while `/metrics` remains available for local inspection. Non-critical observability sidecars can flow through `async_write_jobs` with degradation and drop accounting. Inbound `traceparent` and `X-Trace-ID` headers are parsed into durable trace metadata, checkpoints include trace metadata, MCP and artifact processor execution context can propagate trace headers, and policy audit payloads redact common secret and personal-data patterns.

Example event types:

```text
run_recovered
lease_stolen
policy_denied
tool_retry_suppressed
mcp_context_missing
checkpoint_replay_started
outbound_delivery_failed
```

## Concurrency and Scaling

Duraclaw scales by running multiple workers against the same PostgreSQL database.

Concurrency rules:

- Serialize active runs for the same session by default.
- Allow concurrent runs across different sessions.
- Use leases for runnable work.
- Use customer-level quotas.
- Use agent-instance-level quotas.
- Use tool/MCP concurrency limits.
- Use workflow concurrency limits.
- Use background-job concurrency limits.

Implementation status: database-managed customer and agent-instance runtime limits can hard-fail run, workflow, background-job creation, model-token usage, and model-cost usage when configured quotas are exceeded. User-scoped model token/cost quotas and usage summaries are also supported. Token and cost budgets can be configured for daily, weekly, and monthly periods; model cost is tracked in micro-USD integer units. Background runs are marked distinctly, can store progress, and are exposed through admin/status APIs while reusing the durable run worker. Readiness reports queue depth for runs, outbox, async writes, and due scheduler jobs. Async write sidecars have bounded retry attempts and terminal async write rows are covered by retention cleanup.

PostgreSQL patterns:

- `FOR UPDATE SKIP LOCKED` for run claiming.
- Lease expiry for recovery.
- Idempotency keys for duplicate prevention.
- Transactional outbox for push messages and integration events.

## Initial Implementation Phases

Phase 1: Foundation

- Go module and project layout.
- PostgreSQL migrations.
- Agent instance/session/run schema.
- Artifact metadata and representation schema.
- ACP HTTP server skeleton.
- Nexus-compatible ACP methods.
- Durable run queue and worker leases.

Phase 2: Agent Runtime

- Provider abstraction adapted from PicoClaw.
- Prompt/context builder.
- Tool interface.
- MCP HTTP context propagation.
- Basic model/tool loop.
- Artifact processor interface and customer-context propagation.
- Initial audio, image, and document processor contracts.
- ACP streaming events.

Phase 3: Memory and Knowledge

- Messages, summaries, preferences, memories.
- Agent profile prompt context and scope judging.
- Idle session monitor for compaction, extraction, and active patterns.
- `vector(768)` knowledge chunks.
- Artifact representation retrieval.
- Retrieval pipeline.
- Admin-editable knowledge and memory records.

Phase 4: Scheduling and Background Work

- Shared cron jobs.
- Cron subscriptions.
- Per-user cron jobs.
- Background job mode.
- Long-running artifact processing as background runs.
- Completion push intents for Nexus.

Phase 5: Broadcast and Operations

- Broadcast campaigns.
- Target resolution.
- Outbound messages.
- Nexus delivery status callbacks.
- Quotas, observability, and operational dashboards.

## Non-Goals

Duraclaw v1 should not include:

- Direct channel adapters.
- Channel-specific media fetching or storage protocols.
- External workflow engines.
- Per-user-created agent instances.
- Full human approval workflows beyond user clarification.
- Tool context passed primarily through MCP arguments.
- Filesystem JSONL as primary durability.
