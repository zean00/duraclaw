# Memory, Preferences, and Knowledge

Duraclaw separates conversation history, prompt-fed session context, stable memories, conditional preferences, and knowledge.

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
