CREATE TABLE IF NOT EXISTS customer_runtime_limits (
    customer_id text PRIMARY KEY REFERENCES customers(id) ON DELETE CASCADE,
    max_active_runs integer,
    max_queued_runs integer,
    max_workflow_runs integer,
    max_background_runs integer,
    async_buffer_size integer,
    max_async_payload_bytes integer,
    async_degrade_threshold_bytes integer,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS agent_instance_runtime_limits (
    customer_id text NOT NULL,
    agent_instance_id text NOT NULL,
    max_active_runs integer,
    max_queued_runs integer,
    max_workflow_runs integer,
    max_background_runs integer,
    async_buffer_size integer,
    max_async_payload_bytes integer,
    async_degrade_threshold_bytes integer,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, agent_instance_id),
    FOREIGN KEY (customer_id, agent_instance_id) REFERENCES agent_instances(customer_id, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS async_write_jobs (
    id bigserial PRIMARY KEY,
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    run_id uuid REFERENCES runs(id) ON DELETE SET NULL,
    job_type text NOT NULL,
    target_table text NOT NULL DEFAULT '',
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    state text NOT NULL CHECK (state IN ('queued','leased','completed','failed','dropped','degraded')) DEFAULT 'queued',
    available_at timestamptz NOT NULL DEFAULT now(),
    lease_owner text,
    lease_expires_at timestamptz,
    error text,
    attempts integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);

CREATE INDEX IF NOT EXISTS async_write_jobs_claim_idx ON async_write_jobs (state, available_at, lease_expires_at);
CREATE INDEX IF NOT EXISTS async_write_jobs_customer_state_idx ON async_write_jobs (customer_id, state, created_at);
CREATE INDEX IF NOT EXISTS async_write_jobs_run_idx ON async_write_jobs (run_id, created_at);
