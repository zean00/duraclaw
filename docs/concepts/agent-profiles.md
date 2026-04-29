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
    "scope_judge_model": "openai/gpt-4.1-mini",
    "confidence_threshold": 0.6
  }
}
```

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
