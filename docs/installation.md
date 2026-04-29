# Installation

## Requirements

- Go 1.25 or newer.
- PostgreSQL with `pgcrypto` and `pgvector` available.
- Docker and Docker Compose for the local stack.

The first migration creates the required extensions:

```sql
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS vector;
```

## Local Docker Stack

```bash
docker compose up --build
```

The local stack starts Duraclaw and PostgreSQL. The service listens on the configured `ADDR`, defaulting to `:8080`.

Check health:

```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
```

## Run From Source

```bash
DATABASE_URL=postgres://user:pass@localhost:5432/duraclaw?sslmode=disable \
ADDR=:8080 \
go run ./cmd/duraclaw
```

Startup does the following:

- Connects to PostgreSQL.
- Applies embedded SQL migrations.
- Starts the durable run worker.
- Starts the scheduler loop.
- Starts the idle session monitor.
- Starts the async write worker.
- Starts the outbound outbox worker.
- Exposes ACP and admin HTTP routes.

## Build

```bash
go build ./cmd/duraclaw
```

## Test

```bash
go test ./...
```

Run PostgreSQL and pgvector integration tests against a real database:

```bash
TEST_DATABASE_URL=postgres://user:pass@localhost:5432/duraclaw_test?sslmode=disable \
go test ./internal/db -run "Postgres|Vector" -count=1 -v
```

## Generate This Documentation

Install MkDocs, then serve locally:

```bash
pip install mkdocs
mkdocs serve
```

Build static HTML:

```bash
mkdocs build
```
