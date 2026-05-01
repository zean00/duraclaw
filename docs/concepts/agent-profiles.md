# Agent Profiles

Agent profiles describe how an agent should present itself and what domain it is allowed to answer. Profiles are stored in `profile_config` on immutable agent instance versions, so every run uses the profile snapshot active when the run was created.

Policy packs remain the reusable enforcement and audit mechanism. Profiles are the agent-facing identity and domain configuration.

## Profile Fields

`profile_config` supports:

- `personality`
- `communication_style`
- `language_capabilities`
- `domain_scope.allowed_domains`
- `domain_scope.forbidden_domains`
- `domain_scope.out_of_scope_guidance`
- `domain_scope.scope_judge_model`
- `domain_scope.confidence_threshold`
- `recommendation.enabled`
- `recommendation.timeout_ms`
- `recommendation.model`
- `recommendation.merge_model`
- `recommendation.max_candidates`
- `recommendation.allow_sponsored`
- `recommendation.disclosure_style`

Example:

```json
{
  "personality": "calm, direct, and precise",
  "communication_style": "concise but complete",
  "language_capabilities": ["en", "id"],
  "domain_scope": {
    "allowed_domains": ["customer support", "product usage", "billing explanation"],
    "forbidden_domains": ["legal advice", "medical diagnosis"],
    "out_of_scope_guidance": "Briefly explain that the request is outside scope and offer an allowed alternative.",
    "scope_judge_model": "openrouter/openai/gpt-4.1-mini",
    "confidence_threshold": 0.6
  },
  "recommendation": {
    "enabled": true,
    "timeout_ms": 1500,
    "model": "openrouter/qwen/qwen3.6-35b-a3b",
    "merge_model": "openrouter/openai/gpt-4.1-mini",
    "max_candidates": 5,
    "allow_sponsored": true,
    "disclosure_style": "soft"
  }
}
```

Model refs are parsed as `provider/model`. If an agent is served through OpenRouter only, qualify profile model refs with `openrouter/`, for example `openrouter/openai/gpt-4.1-mini` or `openrouter/qwen/qwen3.6-35b-a3b`. Using `openai/gpt-4.1-mini` selects the Duraclaw `openai` provider, not the OpenRouter model namespace.

## Scope Judge

When a profile defines domain scope, Duraclaw calls an LLM scope judge before the main assistant model, tools, workflows, or MCP calls. The judge returns strict JSON:

```json
{
  "in_scope": true,
  "confidence": 0.94,
  "reason": "The request is about product usage.",
  "recommended_response": ""
}
```

If the request is out of scope or below threshold, Duraclaw:

- Persists scope judge events.
- Inserts the configured out-of-scope assistant response.
- Marks the run completed.
- Skips the main model/tool/workflow/MCP path.

This prevents side effects from out-of-scope requests.

## Recommendations

When `profile_config.recommendation.enabled` is true, Duraclaw starts a recommendation sidecar after a request passes scope validation. `timeout_ms` is required and must be positive when enabled. If the sidecar finishes before timeout, the selected recommendation is merged into the final assistant response by an LLM so it stays non-intrusive. If the sidecar times out, Duraclaw queues a durable recommendation job and can later emit an outbound `recommendation` intent.

The recommendation input reuses scope judgement context selection:

- `direct` intent uses only the current user request.
- `implicit` intent uses the summarized/recent conversation context plus the current request.

Recommendation catalog items and audit logs are customer-scoped through:

- `POST /admin/recommendations/items`
- `GET /admin/recommendations/items?customer_id={customer_id}`
- `PATCH /admin/recommendations/items/{item_id}`
- `DELETE /admin/recommendations/items/{item_id}?customer_id={customer_id}`
- `GET /admin/recommendations/decisions?customer_id={customer_id}`
- `GET /admin/recommendations/jobs?customer_id={customer_id}`

Catalog item fields include `kind`, `title`, `description`, `tags`, `url`, `priority`, `sponsored`, `sponsor_name`, `status`, `valid_from`, `valid_until`, and `metadata`. `decisions` records what the sidecar selected or skipped for a run. `jobs` exposes durable timeout fallback jobs created when inline recommendation selection did not finish before `timeout_ms`.

## Create a Version With a Profile

```bash
curl -X POST http://localhost:8080/admin/agent-instances/agent-1/versions \
  -H 'Content-Type: application/json' \
  -d '{
    "customer_id": "customer-1",
    "name": "Support Agent v1",
    "system_instructions": "Help the user with the product.",
    "profile_config": {
      "personality": "calm and practical",
      "communication_style": "short answers first, details when needed",
      "language_capabilities": ["en", "id"],
      "domain_scope": {
        "allowed_domains": ["product support", "billing"],
        "forbidden_domains": ["legal advice"],
        "out_of_scope_guidance": "Say this is outside support scope and redirect to product support topics."
      }
    },
    "activate_immediately": true
  }'
```

## Import And Export Profiles

Agent instance versions can be imported and exported as JSON or YAML so profile seeds can live as local data files instead of Go code.

YAML example:

```yaml
customer_id: customer-1
agent_instance_id: agent-1
name: Support Agent v1
model_config:
  primary: openrouter/openai/gpt-4.1-mini
system_instructions: Help the user with the product.
profile_config:
  personality: calm and practical
  communication_style: short answers first, details when needed
  language_capabilities: [en, id]
  domain_scope:
    allowed_domains: [product support, billing]
    forbidden_domains: [legal advice]
    out_of_scope_guidance: Say this is outside support scope and redirect to product support topics.
  recommendation:
    enabled: true
    timeout_ms: 1500
    model: openrouter/openai/gpt-4.1-mini
    merge_model: openrouter/openai/gpt-4.1-mini
    max_candidates: 5
    allow_sponsored: true
    disclosure_style: soft
activate_immediately: true
```

CLI:

```bash
DATABASE_URL=postgres://user:pass@localhost:5432/duraclaw?sslmode=disable \
duraclaw agent-config import --file ./agent.yaml --activate

DATABASE_URL=postgres://user:pass@localhost:5432/duraclaw?sslmode=disable \
duraclaw agent-config export --version-id {version_id} --format yaml --file ./agent.yaml
```

Admin HTTP:

```bash
curl -X POST 'http://localhost:8080/admin/agent-instances/agent-1/versions/import?format=yaml' \
  -H 'Content-Type: application/yaml' \
  --data-binary @agent.yaml

curl 'http://localhost:8080/admin/agent-instances/agent-1/versions/{version_id}/export?format=yaml'
```
