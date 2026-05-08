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

Together AI:

```bash
DURACLAW_PROVIDER=together
DURACLAW_PROVIDER_API_KEY=...
DURACLAW_PROVIDER_MODEL=moonshotai/Kimi-K2.5
DURACLAW_PROVIDER_FALLBACKS=mock/duraclaw
```

DeepSeek:

```bash
DURACLAW_PROVIDER=deepseek
DURACLAW_PROVIDER_API_KEY=...
DURACLAW_PROVIDER_MODEL=deepseek-chat
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

OpenAI, OpenRouter, Together, and DeepSeek chat requests use OpenAI-compatible message shapes. Multimodal content parts are passed through when the selected provider/model supports those modalities. ACP run parts can include `text`, `image_url`, `file`, `input_audio`, and `video_url`.

### Multiple chat providers

Duraclaw can register multiple chat providers in one process with `DURACLAW_PROVIDERS`. Keep `DURACLAW_PROVIDER` as the default provider, then add any additional providers as a JSON object keyed by provider name:

```bash
DURACLAW_PROVIDER=deepseek
DURACLAW_PROVIDER_API_KEY=...
DURACLAW_PROVIDER_MODEL=deepseek-v4-pro
DURACLAW_PROVIDERS='{
  "openrouter": {
    "api_key": "...",
    "default_model": "openai/gpt-4.1-mini",
    "referer": "https://your-app.example",
    "title": "Duraclaw"
  },
  "together": {
    "api_key": "...",
    "default_model": "MiniMaxAI/MiniMax-M2.7"
  },
  "openai-compatible": {
    "base_url": "http://localhost:11434/v1",
    "api_key": "...",
    "default_model": "llama3.1"
  }
}'
```

Provider entries support `api_key`, `base_url`, `default_model`, `model`, `referer`, and `title`. `model` is accepted as an alias for `default_model`. `local` is accepted as an alias for `openai-compatible`.

Once providers are registered, agent instance `model_config` can fallback across providers with provider-qualified model refs:

```json
{
  "primary": "deepseek/deepseek-v4-pro",
  "fallbacks": [
    "openrouter/openai/gpt-4.1-mini",
    "together/MiniMaxAI/MiniMax-M2.7",
    "openai-compatible/llama3.1"
  ]
}
```

The first path segment is the Duraclaw provider name, not necessarily the upstream model owner. For example, use `openrouter/openai/gpt-4.1-mini` to call OpenAI's model through OpenRouter.

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
    },
    "moderation": {
      "enabled": true,
      "mode": "hybrid",
      "model": "openrouter/openai/gpt-4.1-mini",
      "confidence_threshold": 0.7
    }
  }
}
```

The first path segment is parsed as the Duraclaw provider. Use `openrouter/openai/gpt-4.1-mini`, not `openai/gpt-4.1-mini`, when the runtime only registers the OpenRouter provider.

For personal-assistant style profiles, use small, low-latency models for short chat, reminders, policy checks, combined scope/moderation judging, and tool selection. Keep reasoning disabled unless a workflow step explicitly needs more deliberation.

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

## Tool Selection

Agent versions can enable a synchronous model-loop tool shortlist after scope judgement and before the main model call. Existing hard controls still apply first: `tool_config.allowed_tools`, `tool_config.disabled_tools`, admin tool-access rules, MCP tool-access rules, prompt-injection blocking, and `pre_tool` / `post_tool` policy checks.

Duraclaw's built-in shortlist metadata is intentionally domain-neutral. Domain phrases such as personal-assistant reminder wording, commerce terms, or customer-specific process language should live in the agent version's `tool_config.tool_metadata`, not in runtime code.

```json
{
  "profile_config": {
    "tool_selection": {
      "enabled": true,
      "mode": "hybrid",
      "method": "intent_classifier",
      "model": "openrouter/openai/gpt-4.1-mini",
      "max_tools": 6,
      "confidence_threshold": 0.65,
      "tool_like_phrases": ["search", "lookup", "cari"],
      "followup_context_phrases": ["what time", "time zone", "jam berapa", "zona waktu"],
      "router_guidance": "Select save_preference for durable user style preferences."
    }
  }
}
```

Modes:

- `disabled`: expose all authorized tools, matching prior behavior.
- `heuristic`: deterministic shortlist only, lowest latency.
- `hybrid`: deterministic shortlist plus router-model fallback when confidence is low.
- `llm`: always use the configured router model after authorization.

Methods:

- `heuristic`: current lexical metadata scorer over tool name, description, tags, trigger phrases, and negative phrases.
- `hypothetical`: experimental query-rewrite scorer. Duraclaw asks the configured model to describe hypothetical tool capabilities needed for the turn, then locally ranks those descriptions against authorized tool descriptions, tags, trigger phrases, and examples. This keeps the existing `llm` router available for benchmarking and fallback.
- `intent_classifier`: experimental intent-label scorer. Duraclaw asks the configured model to classify the turn against the `intent_labels` declared on authorized tools, then locally exposes tools whose labels match above `confidence_threshold`. This is useful when users use slang, mixed language, or indirect wording but the agent owner can define stable business intents such as `create_reminder`, `update_reminder`, or `search_catalog`. If no authorized candidate declares intent labels, Duraclaw falls back to the normal shortlist/router path instead of suppressing every tool.

If `model` is empty, router fallback, hypothetical capability generation, and intent classification use the run's normal `model_config`. Router failures are non-fatal; Duraclaw falls back to the deterministic shortlist and records a `tool_selection.completed` run event. When an embedder is configured, hypothetical ranking caches authorized tool-document embeddings in process and recomputes only the per-turn hypothetical query embeddings. Tool selection only controls which tools are exposed to the main model; Duraclaw does not force `tool_choice: required` solely because one write-capable tool remains visible.

Scope judgement can provide coarse action routing hints before tool selection. Its JSON includes `requires_action: "yes" | "possible" | "no"` and `action_intents`. When `requires_action` is `no`, tool selection exposes no tools for that turn. When action intents are present, tools whose `tool_metadata.intent_labels` match those intents receive a routing boost, while whitelist/blacklist, MCP access, aliases, conflicts, and policy checks still apply.

`tool_like_phrases` controls short-turn detection for obvious tool requests, `followup_context_phrases` controls when a short reply should include recent conversation for tool routing, and `router_guidance` adds trusted, domain-specific instructions to the LLM router prompt. Keep language, slang, customer-domain terms, and personal-assistant routing policy in these fields or in `tool_config.tool_metadata`; runtime defaults stay domain-neutral.

## Tool Correctness Evaluator

The tool evaluator is disabled by default. When enabled, it does not audit every conversation. It first queues only suspicious completed runs using deterministic signals such as failed tools, suppressed tools, required-but-missing tools, or high-confidence tool-selection decisions with no actual tool call. Queued runs are then evaluated by a separate model and stored in `tool_evaluations`.

Global defaults:

| Variable | Default | Description |
| --- | --- | --- |
| `DURACLAW_TOOL_EVALUATOR_ENABLED` | `false` | Enables the background suspicious-run evaluator. |
| `DURACLAW_TOOL_EVALUATOR_MODEL` | empty | Separate evaluator model. Empty falls back to the run's `model_config`. |
| `DURACLAW_TOOL_EVALUATOR_INTERVAL_SECONDS` | `60` | Background evaluator tick interval. |
| `DURACLAW_TOOL_EVALUATOR_LIMIT` | `25` | Runs/evaluations processed per tick. |
| `DURACLAW_TOOL_EVALUATOR_CONFIDENCE_THRESHOLD` | `0.75` | Minimum confidence for queueing/evaluator repair decisions. |
| `DURACLAW_TOOL_EVALUATOR_REPAIR_MODE` | `safe` | `record_only` stores findings only; `safe` may send clarification/correction outbound messages but does not auto-execute side-effect tools. |

Per-agent override:

```json
{
  "profile_config": {
    "tool_evaluator": {
      "enabled": true,
      "model": "openrouter/openai/gpt-4.1-mini",
      "confidence_threshold": 0.8,
      "repair_mode": "safe",
      "options": {
        "max_tokens": 250
      }
    }
  }
}
```

Explicit reply context can quote the referenced original message only when needed:

```json
{
  "profile_config": {
    "reply_context": {
      "quote_original": "when_missing_recent",
      "max_quote_chars": 800
    }
  }
}
```

`quote_original` accepts `never`, `when_missing_recent`, or `always`. The default is `when_missing_recent`, which keeps normal prompt history compact while recovering a capped original-message excerpt when a direct reply points at a message that has already fallen out of recent history or been summarized. Recovered excerpts are labeled as untrusted data; `max_quote_chars` has a hard maximum of 4000.

Main assistant prompt history can be configured by scope intent:

```json
{
  "profile_config": {
    "prompt_context": {
      "direct_history": "none",
      "implicit_history": "summary_and_recent",
      "max_recent_messages": 8
    }
  }
}
```

Supported history modes are `none`, `summary_only`, `recent_only`, and `summary_and_recent`. The default is `summary_and_recent` for both direct and implicit turns to preserve existing behavior. Set `direct_history` to `none` when self-contained requests should not receive ordinary session summary or recent conversation in the main prompt; memories, preferences, policies, trusted runtime context, explicit reply context, knowledge, workflows, and run-local tool results still apply. If these modes differ on an agent without domain scope or LLM moderation, Duraclaw runs the intent classifier so implicit follow-ups still use the configured implicit history mode.

## Decision Eval CLI

Use `cmd/duraclaw-eval` to compare models on the pre-response decisions that most affect runtime behavior:

- Combined scope/moderation judgement: direct versus implicit intent, in-scope versus out-of-scope classification, safe versus unsafe classification, and second-pass implicit context handling.
- Tool selection: selecting the smallest useful tool set for reminders, reminder updates, preferences, and plain chat.

Example with Together AI:

```bash
DURACLAW_EVAL_PROVIDER=together \
DURACLAW_EVAL_API_KEY=... \
DURACLAW_EVAL_MODEL=MiniMaxAI/MiniMax-M2.7 \
go run ./cmd/duraclaw-eval -mode all
```

The command prints one JSON object per case plus a final `summary` object. It exits non-zero when any case fails, which makes it suitable for CI or manual model comparison. Use `-mode scope`, `-mode moderation`, or `-mode tools` to run a slice. `-mode tools -tool-method all` compares `heuristic`, `llm`, `hypothetical`, and `intent_classifier`; pass one method name to isolate a single path. Use `-tool-suite personal_assistant` to run a broader personal assistant-shaped MCP routing suite covering notes, bookmarks, trackers, location, reminders, calendar, Finder, Quran, content, commerce, and guardrail no-tool cases. Use `-reasoning-off -max-tokens 256` for latency-sensitive OpenRouter model comparisons. `-mode moderation` runs only moderation-focused cases, including benign policy mentions and unsafe implicit-context cases. The `summary` includes `scope_score`, `moderation_score`, `tool_score`, case counts, and total latency; `moderation_score` measures only safe/unsafe accuracy and is not diluted by intent or scope-routing points.

Tool metadata can add deterministic domain hints without granting access:

```json
{
  "tool_config": {
    "tool_metadata": {
      "create_reminder": {
        "tags": ["reminder", "schedule", "alarm"],
        "intent_labels": ["create_reminder", "schedule_reminder"],
        "trigger_phrases": ["remind me", "ingatkan", "besok", "tomorrow"],
        "negative_phrases": ["reminder_reference", "change reminder", "update reminder"],
        "conflicts_with": ["remember", "update_reminder"]
      },
      "update_reminder": {
        "tags": ["reminder", "schedule", "update"],
        "intent_labels": ["update_reminder", "reschedule_reminder"],
        "trigger_phrases": ["reminder_reference", "change reminder", "update reminder", "ubah", "ganti"],
        "conflicts_with": ["create_reminder", "remember"]
      },
      "duraclaw.ask_user": {
        "tags": ["clarification", "missing_details"],
        "intent_labels": ["ask_user", "clarify"],
        "trigger_phrases": ["tomorrow morning", "besok pagi"],
        "conflicts_with": ["create_reminder"]
      }
    }
  }
}
```

Use this pattern for any domain preset. Keep the generic runtime neutral, then seed each agent with the phrases and conflicts that match its actual purpose.

## Tool Loop

By default, when the model returns multiple tool calls in one response, Duraclaw executes that batch before the next model call. Agent versions can opt into experimental interleaving:

```json
{
  "tool_config": {
    "interleave_tool_calls": true
  }
}
```

When enabled, Duraclaw executes only the first tool call from a multi-call model response, appends that tool result, and immediately asks the model to continue. The remaining proposed calls from that response are not executed; the model must choose them again after seeing the first result. This adds extra model round trips but allows reasoning between dependent tool calls even when a provider emits several calls upfront. Existing `tool_config.max_iterations` and `tool_config.max_tool_calls_per_run` still bound the run.

## Outbound Delivery

Log sink is the default. Nexus delivery:

```bash
DURACLAW_OUTBOX_SINK=nexus
NEXUS_OUTBOUND_URL=http://nexus.internal/admin/outbound/push
NEXUS_OUTBOUND_BULK_URL=http://nexus.internal/admin/outbound/push/bulk
NEXUS_TOKEN=...
```

If `NEXUS_OUTBOUND_BULK_URL` is configured, the outbox worker groups claimed outbound rows by topic and posts a batch payload. Without it, Duraclaw posts one outbound intent per request to `NEXUS_OUTBOUND_URL`.

Outbound payloads include `acp_session_id` and a legacy `session_id` alias for compatibility. Current Nexus treats both as the Duraclaw ACP session ID on `/admin/outbound/push`, fans out to every mapped Nexus channel session by default, and honors `channel_type` only when Duraclaw or an external caller needs channel-specific delivery. Duraclaw omits `channel_type` for channel-neutral outbound intents; empty channel values should be treated as omitted. Reminder subscriptions use the same mechanism: omit `channel_type` for all active channels on the ACP session, or set it for a channel-specific reminder. Nexus exposes `/admin/sessions/by-acp?acp_session_id=...` to inspect mapped Nexus sessions and channel availability.

Delivery failures are logged by the outbox worker and released for retry. `/readyz` exposes `outbox_pending`, `outbox_unclaimed`, `outbox_claimed`, and `outbox_stale`; use these fields to detect a stopped worker, stuck sink call, or expired claim lease during local Nexus validation.

## Session Monitor

| Variable | Default | Description |
| --- | --- | --- |
| `DURACLAW_SESSION_MONITOR_INTERVAL_SECONDS` | `60` | How often to scan idle sessions. |
| `DURACLAW_SESSION_MONITOR_IDLE_SECONDS` | `1800` | Idle duration before a session is eligible. |
| `DURACLAW_SESSION_MONITOR_LIMIT` | `25` | Sessions claimed per tick. |
| `DURACLAW_SESSION_MONITOR_MESSAGE_LIMIT` | `40` | Recent messages loaded for compaction/extraction. |
| `DURACLAW_SESSION_COMPACTION_THRESHOLD_CHARS` | `12000` | Transcript size before summary compaction. |
| `DURACLAW_PROFILE_CONSOLIDATION_ENABLED` | `true` | After idle memory/preference extraction, ask the configured model to merge clear duplicate profile items and delete only explicitly identified duplicate IDs. Runs in the background/session-monitor path, not the live turn path. |

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

Duraclaw also uses trusted runtime time metadata to interpret relative dates. The current-time prompt and `duraclaw.current_time` tool prefer a valid IANA timezone from active location, user metadata/profile, or base location. Invalid stored timezones are ignored for trusted defaults and fall back to UTC; an explicit invalid `timezone` argument passed by the model to `duraclaw.current_time` still returns an error so bad tool arguments are visible.

## Observability

```bash
DURACLAW_OTLP_ENDPOINT=http://otel-collector:4318
DURACLAW_OTLP_HEADERS=Authorization=Bearer token
DURACLAW_OTEL_SERVICE_NAME=duraclaw
DURACLAW_OTEL_EXPORT_INTERVAL_SECONDS=10
DURACLAW_OTEL_INSECURE=true
```
