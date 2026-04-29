# Artifacts and Media

Artifacts are durable records for input and output media. Nexus owns channel-specific fetching and delivery; Duraclaw stores channel-neutral metadata and derived representations.

## Artifact Lifecycle

```text
Nexus receives media
  -> Nexus stores media or creates a storage reference
  -> Nexus sends artifact metadata to Duraclaw
  -> Duraclaw persists artifact record
  -> processor creates representations
  -> prompt builder uses representations
  -> agent or workflow continues
```

## Artifact States

- `pending`
- `available`
- `processing`
- `processed`
- `failed`
- `expired`
- `deleted`

## Representations

Representations are derived views of an artifact:

- `transcript`
- `ocr_text`
- `vision_summary`
- `document_text`
- `document_outline`
- `structured_fields`
- `thumbnail`
- `embedding`
- `moderation_result`
- `metadata_summary`
- `video_summary`

The runtime consumes representations instead of raw binary payloads whenever possible.

## Processors

Duraclaw supports:

- Mock processor for local development.
- HTTP processor for OCR/transcription/document extraction services.
- Provider-backed processor through OpenAI, OpenRouter, or OpenAI-compatible multimodal chat.

Processor calls persist intent before processing and result/error after processing.

## Generated Media

Provider-level image, audio, and video generation capabilities can create durable generated artifact records. Media blobs can be stored through file or HTTP PUT storage adapters.
