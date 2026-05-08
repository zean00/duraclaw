CREATE TABLE IF NOT EXISTS tool_evaluations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    customer_id text NOT NULL,
    user_id text NOT NULL,
    agent_instance_id text NOT NULL,
    session_id text NOT NULL,
    status text NOT NULL CHECK (status IN ('queued','running','completed','failed')),
    category text NOT NULL DEFAULT '',
    confidence double precision NOT NULL DEFAULT 0,
    expected_tools jsonb NOT NULL DEFAULT '[]'::jsonb,
    actual_tools jsonb NOT NULL DEFAULT '[]'::jsonb,
    reason text NOT NULL DEFAULT '',
    repair_action text NOT NULL DEFAULT '',
    repair_status text NOT NULL DEFAULT '',
    finding jsonb NOT NULL DEFAULT '{}'::jsonb,
    suspicious_signals jsonb NOT NULL DEFAULT '[]'::jsonb,
    lease_owner text,
    lease_expires_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id)
);

CREATE INDEX IF NOT EXISTS tool_evaluations_claim_idx
    ON tool_evaluations (status, lease_expires_at, created_at);

CREATE INDEX IF NOT EXISTS tool_evaluations_customer_status_idx
    ON tool_evaluations (customer_id, status, created_at DESC);
