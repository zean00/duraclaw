CREATE TABLE IF NOT EXISTS agent_delegation_handles (
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    handle text NOT NULL,
    agent_instance_id text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, handle),
    FOREIGN KEY (customer_id, agent_instance_id) REFERENCES agent_instances(customer_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS agent_delegation_handles_agent_idx
    ON agent_delegation_handles (customer_id, agent_instance_id);

CREATE TABLE IF NOT EXISTS agent_delegation_access_rules (
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    agent_instance_id text NOT NULL,
    user_id text NOT NULL DEFAULT '',
    allowed_agents jsonb NOT NULL DEFAULT '[]'::jsonb,
    denied_agents jsonb NOT NULL DEFAULT '[]'::jsonb,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, agent_instance_id, user_id),
    FOREIGN KEY (customer_id, agent_instance_id) REFERENCES agent_instances(customer_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS agent_delegation_access_customer_agent_idx
    ON agent_delegation_access_rules (customer_id, agent_instance_id, user_id);

CREATE TABLE IF NOT EXISTS agent_delegations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    user_id text NOT NULL,
    source_agent_instance_id text NOT NULL,
    target_agent_instance_id text NOT NULL,
    target_handle text NOT NULL,
    parent_session_id text NOT NULL,
    parent_run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    child_session_id text NOT NULL,
    child_run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    exact_message text NOT NULL DEFAULT '',
    context_summary text NOT NULL DEFAULT '',
    status text NOT NULL CHECK (status IN ('queued','running','completed','failed','cancelled')),
    result_text text NOT NULL DEFAULT '',
    error text,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);

CREATE INDEX IF NOT EXISTS agent_delegations_parent_run_idx
    ON agent_delegations (parent_run_id);

CREATE INDEX IF NOT EXISTS agent_delegations_child_run_idx
    ON agent_delegations (child_run_id);

CREATE INDEX IF NOT EXISTS agent_delegations_parent_session_idx
    ON agent_delegations (customer_id, parent_session_id, created_at DESC);
