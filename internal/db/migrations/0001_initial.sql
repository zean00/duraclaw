CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS schema_migrations (
    version integer PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS customers (
    id text PRIMARY KEY,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS users (
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    id text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, id)
);

CREATE TABLE IF NOT EXISTS agent_instances (
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    id text NOT NULL,
    name text NOT NULL DEFAULT 'Duraclaw Agent',
    model_config jsonb NOT NULL DEFAULT '{}'::jsonb,
    system_instructions text NOT NULL DEFAULT '',
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, id)
);

CREATE TABLE IF NOT EXISTS agent_policies (
    customer_id text NOT NULL,
    agent_instance_id text NOT NULL,
    artifact_max_size_bytes bigint NOT NULL DEFAULT 104857600,
    artifact_media_types jsonb NOT NULL DEFAULT '[]'::jsonb,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, agent_instance_id),
    FOREIGN KEY (customer_id, agent_instance_id) REFERENCES agent_instances(customer_id, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS sessions (
    customer_id text NOT NULL,
    user_id text NOT NULL,
    agent_instance_id text NOT NULL,
    id text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, id),
    FOREIGN KEY (customer_id, user_id) REFERENCES users(customer_id, id) ON DELETE CASCADE,
    FOREIGN KEY (customer_id, agent_instance_id) REFERENCES agent_instances(customer_id, id)
);

CREATE TABLE IF NOT EXISTS messages (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id text NOT NULL,
    session_id text NOT NULL,
    run_id uuid,
    role text NOT NULL,
    content jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (customer_id, session_id) REFERENCES sessions(customer_id, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id text NOT NULL,
    user_id text NOT NULL,
    agent_instance_id text NOT NULL,
    session_id text NOT NULL,
    request_id text NOT NULL,
    idempotency_key text NOT NULL,
    state text NOT NULL CHECK (state IN ('queued','leased','running','running_workflow','awaiting_user','completed','failed','cancelled','expired')),
    input jsonb NOT NULL DEFAULT '{}'::jsonb,
    channel_context jsonb NOT NULL DEFAULT '{}'::jsonb,
    lease_owner text,
    leased_at timestamptz,
    lease_expires_at timestamptz,
    error text,
    final_message_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    UNIQUE (customer_id, session_id, idempotency_key),
    FOREIGN KEY (customer_id, session_id) REFERENCES sessions(customer_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS runs_claim_idx ON runs (state, lease_expires_at, created_at);
CREATE INDEX IF NOT EXISTS runs_session_state_idx ON runs (customer_id, session_id, state);

CREATE TABLE IF NOT EXISTS run_steps (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    kind text NOT NULL,
    state text NOT NULL CHECK (state IN ('pending','running','succeeded','failed','skipped','cancelled')),
    input jsonb NOT NULL DEFAULT '{}'::jsonb,
    output jsonb NOT NULL DEFAULT '{}'::jsonb,
    error text,
    started_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS run_events (
    id bigserial PRIMARY KEY,
    run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    event_type text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS run_events_run_id_id_idx ON run_events (run_id, id);

CREATE TABLE IF NOT EXISTS checkpoints (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    checkpoint_key text NOT NULL,
    state jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS artifacts (
    id text PRIMARY KEY,
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    run_id uuid REFERENCES runs(id) ON DELETE SET NULL,
    modality text NOT NULL,
    media_type text NOT NULL DEFAULT '',
    filename text NOT NULL DEFAULT '',
    size_bytes bigint NOT NULL DEFAULT 0,
    checksum text NOT NULL DEFAULT '',
    storage_ref text NOT NULL DEFAULT '',
    source_channel text NOT NULL DEFAULT '',
    source_message_id text NOT NULL DEFAULT '',
    state text NOT NULL CHECK (state IN ('pending','available','processing','processed','failed','expired','deleted')),
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS artifact_representations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    artifact_id text NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
    representation_type text NOT NULL,
    summary text NOT NULL DEFAULT '',
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    embedding vector(768),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS memories (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    user_id text NOT NULL,
    session_id text,
    memory_type text NOT NULL,
    content text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    embedding vector(768),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS memories_customer_user_idx ON memories (customer_id, user_id, memory_type, updated_at);

CREATE TABLE IF NOT EXISTS knowledge_documents (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    title text NOT NULL DEFAULT '',
    source_ref text NOT NULL DEFAULT '',
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS knowledge_chunks (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id uuid NOT NULL REFERENCES knowledge_documents(id) ON DELETE CASCADE,
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    chunk_index integer NOT NULL,
    content text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    embedding vector(768),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (document_id, chunk_index)
);

CREATE INDEX IF NOT EXISTS knowledge_chunks_customer_idx ON knowledge_chunks (customer_id, document_id, chunk_index);

CREATE TABLE IF NOT EXISTS processor_calls (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    artifact_id text NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
    run_id uuid REFERENCES runs(id) ON DELETE SET NULL,
    processor text NOT NULL,
    state text NOT NULL,
    request_summary jsonb NOT NULL DEFAULT '{}'::jsonb,
    response_summary jsonb NOT NULL DEFAULT '{}'::jsonb,
    error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);

CREATE INDEX IF NOT EXISTS processor_calls_run_idx ON processor_calls (run_id, created_at);

CREATE TABLE IF NOT EXISTS model_calls (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    provider text NOT NULL,
    model text NOT NULL,
    state text NOT NULL,
    request_summary jsonb NOT NULL DEFAULT '{}'::jsonb,
    response_summary jsonb NOT NULL DEFAULT '{}'::jsonb,
    error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);

CREATE INDEX IF NOT EXISTS model_calls_run_idx ON model_calls (run_id, created_at);

CREATE TABLE IF NOT EXISTS tool_calls (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    tool_name text NOT NULL,
    state text NOT NULL,
    arguments jsonb NOT NULL DEFAULT '{}'::jsonb,
    result jsonb NOT NULL DEFAULT '{}'::jsonb,
    retryable boolean NOT NULL DEFAULT true,
    error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);

CREATE INDEX IF NOT EXISTS tool_calls_run_idx ON tool_calls (run_id, created_at);

CREATE TABLE IF NOT EXISTS mcp_calls (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    server_name text NOT NULL,
    tool_name text NOT NULL,
    state text NOT NULL,
    request_summary jsonb NOT NULL DEFAULT '{}'::jsonb,
    response_summary jsonb NOT NULL DEFAULT '{}'::jsonb,
    error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);

CREATE INDEX IF NOT EXISTS mcp_calls_run_idx ON mcp_calls (run_id, created_at);

CREATE TABLE IF NOT EXISTS workflow_definitions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    version integer NOT NULL,
    status text NOT NULL,
    description text NOT NULL DEFAULT '',
    when_to_use text NOT NULL DEFAULT '',
    input_schema jsonb NOT NULL DEFAULT '{}'::jsonb,
    output_schema jsonb NOT NULL DEFAULT '{}'::jsonb,
    owner_scope text NOT NULL DEFAULT 'customer',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (name, version)
);

CREATE TABLE IF NOT EXISTS workflow_nodes (
    workflow_definition_id uuid NOT NULL REFERENCES workflow_definitions(id) ON DELETE CASCADE,
    node_key text NOT NULL,
    node_type text NOT NULL,
    config jsonb NOT NULL DEFAULT '{}'::jsonb,
    retry_policy jsonb NOT NULL DEFAULT '{}'::jsonb,
    timeout_policy jsonb NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (workflow_definition_id, node_key)
);

CREATE TABLE IF NOT EXISTS workflow_edges (
    workflow_definition_id uuid NOT NULL REFERENCES workflow_definitions(id) ON DELETE CASCADE,
    from_node_key text NOT NULL,
    to_node_key text NOT NULL,
    condition jsonb NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (workflow_definition_id, from_node_key, to_node_key)
);

CREATE TABLE IF NOT EXISTS workflow_assignments (
    workflow_definition_id uuid NOT NULL REFERENCES workflow_definitions(id) ON DELETE CASCADE,
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    agent_instance_id text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    PRIMARY KEY (workflow_definition_id, customer_id, agent_instance_id)
);

CREATE TABLE IF NOT EXISTS workflow_runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    workflow_definition_id uuid NOT NULL REFERENCES workflow_definitions(id),
    workflow_version integer NOT NULL,
    status text NOT NULL CHECK (status IN ('queued','running','awaiting_user','succeeded','failed','cancelled','expired')),
    current_node_key text NOT NULL DEFAULT '',
    input jsonb NOT NULL DEFAULT '{}'::jsonb,
    output jsonb NOT NULL DEFAULT '{}'::jsonb,
    error text,
    started_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);

CREATE TABLE IF NOT EXISTS workflow_node_runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_run_id uuid NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
    node_key text NOT NULL,
    status text NOT NULL CHECK (status IN ('queued','running','awaiting_user','succeeded','failed','cancelled','expired')),
    attempts integer NOT NULL DEFAULT 0,
    input_ref jsonb NOT NULL DEFAULT '{}'::jsonb,
    output_ref jsonb NOT NULL DEFAULT '{}'::jsonb,
    error text,
    started_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS workflow_node_states (
    workflow_run_id uuid NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
    node_key text NOT NULL,
    status text NOT NULL CHECK (status IN ('pending','queued','running','awaiting_user','succeeded','skipped','failed','cancelled','expired')),
    output jsonb NOT NULL DEFAULT '{}'::jsonb,
    error text,
    attempts integer NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workflow_run_id, node_key)
);

CREATE TABLE IF NOT EXISTS workflow_edge_activations (
    workflow_run_id uuid NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
    from_node_key text NOT NULL,
    to_node_key text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workflow_run_id, from_node_key, to_node_key)
);

CREATE INDEX IF NOT EXISTS workflow_node_states_status_idx ON workflow_node_states (workflow_run_id, status);
CREATE INDEX IF NOT EXISTS workflow_edge_activations_to_idx ON workflow_edge_activations (workflow_run_id, to_node_key, active);

CREATE TABLE IF NOT EXISTS outbound_intents (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    user_id text NOT NULL,
    session_id text NOT NULL,
    run_id uuid REFERENCES runs(id) ON DELETE SET NULL,
    intent_type text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL CHECK (status IN ('pending','queued','sent','failed','cancelled')) DEFAULT 'pending',
    outbox_id bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS outbound_intents_customer_status_idx ON outbound_intents (customer_id, status, created_at);

CREATE TABLE IF NOT EXISTS broadcasts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    title text NOT NULL DEFAULT '',
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    generation_mode text NOT NULL DEFAULT 'direct',
    agent_instance_id text,
    generation_request jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL CHECK (status IN ('draft','queued','sent','failed','cancelled')) DEFAULT 'queued',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS broadcast_targets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    broadcast_id uuid NOT NULL REFERENCES broadcasts(id) ON DELETE CASCADE,
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    user_id text NOT NULL,
    session_id text NOT NULL,
    status text NOT NULL CHECK (status IN ('pending','queued','sent','failed','cancelled')) DEFAULT 'pending',
    outbound_intent_id uuid REFERENCES outbound_intents(id) ON DELETE SET NULL,
    generation_run_id uuid REFERENCES runs(id) ON DELETE SET NULL,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS broadcasts_customer_status_idx ON broadcasts (customer_id, status, created_at);
CREATE INDEX IF NOT EXISTS broadcast_targets_broadcast_status_idx ON broadcast_targets (broadcast_id, status);

CREATE TABLE IF NOT EXISTS async_outbox (
    id bigserial PRIMARY KEY,
    topic text NOT NULL,
    payload jsonb NOT NULL,
    available_at timestamptz NOT NULL DEFAULT now(),
    claimed_at timestamptz,
    claim_owner text,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS async_outbox_claim_idx ON async_outbox (completed_at, available_at, claimed_at);

CREATE TABLE IF NOT EXISTS observability_events (
    id bigserial PRIMARY KEY,
    customer_id text NOT NULL,
    run_id uuid,
    event_type text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS observability_events_customer_created_idx ON observability_events (customer_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS observability_events_run_idx ON observability_events (run_id, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS scheduler_jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    schedule text NOT NULL,
    next_run_at timestamptz NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    enabled boolean NOT NULL DEFAULT true,
    lease_owner text,
    lease_expires_at timestamptz,
    last_fired_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS scheduler_jobs_claim_idx ON scheduler_jobs (enabled, next_run_at, lease_expires_at);
