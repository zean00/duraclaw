# Memory, Preferences, and Knowledge

Duraclaw separates conversation history, prompt-fed session context, stable memories, conditional preferences, and knowledge.

## User Metadata and External Profiles

Duraclaw keeps canonical user profile data out of the core schema. The built-in `users` table has a flexible `metadata` JSON object. Deployments can configure an optional customer profile retriever to refresh external profile data into:

```json
{
  "profile": {
    "display_name": "Sahal",
    "timezone": "Asia/Jakarta",
    "locale": "id-ID"
  },
  "profile_source": {
    "provider": "customer_profile_retriever",
    "retrieved_at": "2026-04-29T10:00:00Z"
  }
}
```

Prompt context includes only explicitly allowlisted profile fields. Memories and preferences remain separate from profile data:

- Profile data is external or customer-canonical identity/context.
- Memories are stable facts learned or saved through assistant usage.
- Preferences are conditional choices.

## Conversation History

Full conversation history lives in `messages`. It is the audit/source record and is not itself the entire prompt context.

## Session Context

Prompt-fed session context lives in `session_summaries`. The idle session monitor updates summaries after a session has been idle long enough or when the transcript exceeds the compaction threshold.

This allows one long persisted session while keeping model context bounded.

## Memories

Memories are stable user facts that rarely change.

Examples:

- “The user works at Acme.”
- “The user has a daughter named Rina.”
- “The user lives in Jakarta.”

Memory writes are policy-controlled and can be created by tools, workflows, admin APIs, or idle extraction.

## Preferences

Preferences are user choices that may be conditional.

Examples:

- “Prefers ice cream in summer.”
- “Prefers hot chocolate in winter.”
- “Prefers short responses during work hours.”

Preferences include a `condition` JSON object. Prompt context includes only preferences that match the current context, such as season, month, or hour.

## Knowledge

Knowledge is admin-managed content for retrieval:

- Shared knowledge available across customers.
- Customer knowledge scoped to one `customer_id`.

Knowledge chunks support text search and pgvector embeddings with dimension 768.

## Idle Extraction

The session monitor uses the configured provider as a JSON extractor to derive:

- Stable memories.
- Conditional preferences.
- Summary updates.
- Active-time patterns.

It deduplicates conservatively and records provenance metadata.
