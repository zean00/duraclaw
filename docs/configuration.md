# Configuration

Duraclaw is configured with environment variables.

## Required

| Variable | Description |
| --- | --- |
| `DATABASE_URL` | PostgreSQL connection string. |

## Server

| Variable | Default | Description |
| --- | --- | --- |
| `ADDR` | `:8080` | HTTP listen address. |
| `HOSTNAME` | empty | Worker owner name used for leases and jobs. |

## Authentication

Admin and ACP routes are open by default for local development.

```bash
DURACLAW_ADMIN_TOKEN=...
DURACLAW_ACP_TOKEN=...
DURACLAW_REQUIRE_AUTH=true
```

When enabled, admin routes require the admin bearer token and ACP routes require the ACP bearer token.

## Chat Provider

The default provider is `mock`.

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

OpenAI-compatible or local LLM:

```bash
DURACLAW_PROVIDER=openai-compatible
DURACLAW_PROVIDER_BASE_URL=http://localhost:11434/v1
DURACLAW_PROVIDER_API_KEY=...
DURACLAW_PROVIDER_MODEL=llama3.1
DURACLAW_PROVIDER_FALLBACKS=mock/duraclaw
```

OpenAI and OpenRouter chat requests support multimodal message content arrays. ACP run parts can include `text`, `image_url`, `file`, `input_audio`, and `video_url`.

### OpenRouter model guidance

Agent instance `model_config.primary`, `model_config.fallbacks`, and `profile_config.domain_scope.scope_judge_model` may use provider-qualified model refs:

```json
{
  "model_config": {
    "primary": "openrouter/openai/gpt-4.1-mini",
    "fallbacks": []
  },
  "profile_config": {
    "domain_scope": {
      "scope_judge_model": "openrouter/openai/gpt-4.1-mini"
    }
  }
}
```

The first path segment is parsed as the Duraclaw provider. Use `openrouter/openai/gpt-4.1-mini`, not `openai/gpt-4.1-mini`, when the runtime only registers the OpenRouter provider.

For personal-assistant style profiles, use small, low-latency models for short chat, reminders, policy checks, and scope judging. Keep reasoning disabled unless a workflow step explicitly needs more deliberation.

| Use case | Recommended model config |
| --- | --- |
| Default baseline | `openrouter/openai/gpt-4.1-mini` |
| Qwen low-latency candidate | `openrouter/qwen/qwen3.6-35b-a3b` with reasoning disabled |
| Qwen broader-context candidate | `openrouter/qwen/qwen3.6-27b` with reasoning disabled |
| Complex workflow planning only | Consider limited reasoning on `qwen/qwen3.6-35b-a3b` |

Recommended Qwen config for short chat, reminders, policy checks, and scope judging:

```json
{
  "primary": "openrouter/qwen/qwen3.6-35b-a3b",
  "fallbacks": ["openrouter/openai/gpt-4.1-mini"],
  "options": {
    "max_tokens": 320,
    "reasoning": {
      "effort": "none",
      "exclude": true
    }
  }
}
```

If a workflow needs more deliberation, use a small reasoning budget rather than unbounded reasoning:

```json
{
  "max_tokens": 512,
  "reasoning": {
    "max_tokens": 128,
    "exclude": true
  }
}
```

Reasoning tokens count as output tokens on OpenRouter. Prefer a small reasoning budget over unrestricted reasoning for latency-sensitive assistant paths.

## Embeddings

Default embeddings use a deterministic local hash provider for tests and local development.

OpenAI-compatible embeddings:

```bash
DURACLAW_EMBEDDING_PROVIDER=openai-compatible
DURACLAW_EMBEDDING_BASE_URL=https://api.openai.com/v1
DURACLAW_EMBEDDING_API_KEY=...
DURACLAW_EMBEDDING_MODEL=text-embedding-3-small
DURACLAW_EMBEDDING_DIMENSIONS=768
```

OpenRouter embeddings:

```bash
DURACLAW_EMBEDDING_PROVIDER=openrouter
DURACLAW_EMBEDDING_API_KEY=...
DURACLAW_EMBEDDING_MODEL=openai/text-embedding-3-small
```

## Artifact Processors

HTTP artifact processor:

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

Provider-backed processor:

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

OpenRouter audio transcription uses OpenRouter's `/audio/transcriptions` STT endpoint, defaults to `openai/whisper-large-v3` unless artifact metadata supplies `transcription_model` or `model`, and persists a `transcript` representation. Provider processors use multimodal chat prompts for other modalities and persist artifact representations such as `vision_summary`, `document_text`, `transcript`, and `video_summary`.

## Generated Media Storage

File storage:

```bash
DURACLAW_GENERATED_MEDIA_DIR=/var/lib/duraclaw/generated-media
DURACLAW_GENERATED_MEDIA_REF_PREFIX=file:///var/lib/duraclaw/generated-media
```

HTTP PUT storage:

```bash
DURACLAW_GENERATED_MEDIA_HTTP_PUT_URL=https://storage.example/upload-target
DURACLAW_GENERATED_MEDIA_HTTP_BASE_URL=https://cdn.example/generated
DURACLAW_GENERATED_MEDIA_HTTP_HEADERS=Authorization=Bearer token
```

## MCP

Global MCP servers:

```bash
DURACLAW_MCP_CONFIG='{"servers":[{"name":"tools","transport":"http","base_url":"http://mcp.internal"}]}'
```

Agent instance versions may also define `mcp_config.servers`.

## Outbound Delivery

Log sink is the default. Nexus delivery:

```bash
DURACLAW_OUTBOX_SINK=nexus
NEXUS_OUTBOUND_URL=http://nexus.internal/acp/outbound
NEXUS_OUTBOUND_BULK_URL=http://nexus.internal/acp/outbound/bulk
NEXUS_TOKEN=...
```

If `NEXUS_OUTBOUND_BULK_URL` is configured, the outbox worker groups claimed outbound rows by topic and posts a batch payload. Without it, Duraclaw posts one outbound intent per request to `NEXUS_OUTBOUND_URL`.

Delivery failures are logged by the outbox worker and released for retry. `/readyz` exposes `outbox_pending`, `outbox_unclaimed`, `outbox_claimed`, and `outbox_stale`; use these fields to detect a stopped worker, stuck sink call, or expired claim lease during local Nexus validation.

## Session Monitor

| Variable | Default | Description |
| --- | --- | --- |
| `DURACLAW_SESSION_MONITOR_INTERVAL_SECONDS` | `60` | How often to scan idle sessions. |
| `DURACLAW_SESSION_MONITOR_IDLE_SECONDS` | `1800` | Idle duration before a session is eligible. |
| `DURACLAW_SESSION_MONITOR_LIMIT` | `25` | Sessions claimed per tick. |
| `DURACLAW_SESSION_MONITOR_MESSAGE_LIMIT` | `40` | Recent messages loaded for compaction/extraction. |
| `DURACLAW_SESSION_COMPACTION_THRESHOLD_CHARS` | `12000` | Transcript size before summary compaction. |

## Rapid Follow-Up Refinement

| Variable | Default | Description |
| --- | --- | --- |
| `DURACLAW_RUN_INTERRUPT_WINDOW_MS` | `2000` | Window from pipeline start where same-session follow-up messages are deferred and folded into a refinement run. |
| `DURACLAW_RUN_MAX_REFINEMENT_DEPTH` | `2` | Maximum chained refinement runs. Set to `0` to disable rapid follow-up deferral. |

## Agent Activity Signals

Duraclaw can emit user-visible activity intents while a run is processing. When enabled, matching activity events are written as durable run events and as outbound intents with `intent_type: "agent_activity"` for Nexus to map into channel-specific status UI.

| Variable | Default | Description |
| --- | --- | --- |
| `DURACLAW_AGENT_ACTIVITY_ENABLED` | `false` | Enables outbound `agent_activity` intents and matching durable `agent_activity.*` run events. |
| `DURACLAW_AGENT_ACTIVITY_INCLUDE` | empty | Comma-separated allow list. Empty means all supported activity types. Supported values: `thinking`, `scope`, `context`, `workflow`, `model`, `tool`, `artifact`, `refinement`. |
| `DURACLAW_AGENT_ACTIVITY_OMIT` | empty | Comma-separated deny list applied after include. Use this to suppress noisy types such as `model` or `tool`. |

## Customer Profile Retriever

Duraclaw does not create a dedicated user-profile table. Optional external customer profile data is refreshed into `users.metadata.profile`.

HTTP profile retriever:

```bash
DURACLAW_CUSTOMER_PROFILE_URL=https://customer.example/profile
DURACLAW_CUSTOMER_PROFILE_TOKEN=...
DURACLAW_CUSTOMER_PROFILE_HEADERS=X-App=duraclaw
DURACLAW_CUSTOMER_PROFILE_TIMEOUT_SECONDS=5
DURACLAW_CUSTOMER_PROFILE_PROMPT_FIELDS=display_name,timezone,locale
```

The retriever is called on ACP session ensure and run creation. It receives customer/user/session/agent context and should return:

```json
{
  "profile": {
    "display_name": "Sahal",
    "timezone": "Asia/Jakarta",
    "locale": "id-ID"
  },
  "metadata": {
    "source": "crm"
  }
}
```

Only fields listed in `DURACLAW_CUSTOMER_PROFILE_PROMPT_FIELDS` are included in model prompt context. Sensitive fields such as email, phone, and birth date should remain omitted unless explicitly needed.

## Observability

```bash
DURACLAW_OTLP_ENDPOINT=http://otel-collector:4318
DURACLAW_OTLP_HEADERS=Authorization=Bearer token
DURACLAW_OTEL_SERVICE_NAME=duraclaw
DURACLAW_OTEL_EXPORT_INTERVAL_SECONDS=10
DURACLAW_OTEL_INSECURE=true
```
