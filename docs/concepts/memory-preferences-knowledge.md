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

Admins can force compaction for a specific session with `POST /admin/sessions/{session_id}/compact`. The endpoint runs the same LLM summarization path used by the idle monitor, persists the generated summary, and returns it in the response.

This allows one long persisted session while keeping model context bounded.

## Memories

Memories are stable user facts that rarely change.

Examples:

- “The user works at Acme.”
- “The user has a daughter named Rina.”
- “The user lives in Jakarta.”

Memory writes are policy-controlled and can be created by tools, workflows, admin APIs, or idle extraction.

When the model-loop `remember` tool writes a memory, the tool result includes a `memory_reference` artifact with the memory ID plus admin update/delete route references.

Prompt context injects a bounded, relevance-ranked set of memories. Duraclaw ranks candidates against the current user message plus the session summary using semantic embeddings when available, text overlap as a fallback, usage frequency, and recency. The cap is not simply the most recently written memories.

## Preferences

Preferences are user choices that may be conditional.

Examples:

- “Prefers ice cream in summer.”
- “Prefers hot chocolate in winter.”
- “Prefers short responses during work hours.”

Preferences include a `condition` JSON object. Prompt context includes only preferences that match the current context, such as season, month, or hour.

When the model-loop `save_preference` tool writes a preference, the tool result includes a `preference_reference` artifact with the preference ID plus admin update/delete route references. The runtime prompt instructs the agent not to claim a preference was saved unless the `save_preference` tool succeeds; user-visible "saved" confirmations should therefore include the tool artifact in the final outbound message.

Prompt context ranks matching preferences by relevance before injection. A preference's usage counter is updated when it is selected for prompt context; explicit list APIs still return stored preferences for inspection and management.

## Knowledge

Knowledge is admin-managed content for retrieval:

- Shared knowledge available across customers.
- Customer knowledge scoped to one `customer_id`.

Knowledge chunks support text search and pgvector embeddings with dimension 768.

Admin APIs manage both customer and shared knowledge:

- `POST /admin/knowledge/text`
- `GET /admin/knowledge/documents?customer_id={customer_id}&scope=customer|shared|all`
- `GET /admin/knowledge/documents/{document_id}/chunks`
- `DELETE /admin/knowledge/documents/{document_id}?customer_id={customer_id}`

Set `scope` to `shared` when ingesting text that should be available across customers. Set `scope` to `customer` or omit it for customer-specific content. Runtime retrieval includes both the current customer's knowledge and shared knowledge.

## Idle Extraction

The session monitor uses the configured provider as a JSON extractor to derive:

- Stable memories.
- Conditional preferences.
- Summary updates.
- Active-time patterns.

It deduplicates conservatively and records provenance metadata.
