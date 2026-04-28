CREATE TABLE IF NOT EXISTS session_agent_instance_transfers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id text NOT NULL,
    session_id text NOT NULL,
    from_agent_instance_id text NOT NULL,
    to_agent_instance_id text NOT NULL,
    reason text NOT NULL DEFAULT '',
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (customer_id, session_id) REFERENCES sessions(customer_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS session_transfers_session_created_idx ON session_agent_instance_transfers (customer_id, session_id, created_at DESC);

ALTER TABLE reminder_subscriptions ADD COLUMN IF NOT EXISTS lease_owner text;
ALTER TABLE reminder_subscriptions ADD COLUMN IF NOT EXISTS lease_expires_at timestamptz;

CREATE INDEX IF NOT EXISTS reminder_subscriptions_claim_idx ON reminder_subscriptions (enabled, next_run_at, lease_expires_at);
