CREATE TABLE IF NOT EXISTS session_summaries (
    customer_id text NOT NULL,
    session_id text NOT NULL,
    summary text NOT NULL DEFAULT '',
    source_run_id uuid REFERENCES runs(id) ON DELETE SET NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, session_id),
    FOREIGN KEY (customer_id, session_id) REFERENCES sessions(customer_id, id) ON DELETE CASCADE
);

ALTER TABLE runs ADD COLUMN IF NOT EXISTS run_mode text NOT NULL DEFAULT 'interactive';
ALTER TABLE runs ADD COLUMN IF NOT EXISTS progress jsonb NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS runs_background_idx ON runs (customer_id, agent_instance_id, state, created_at) WHERE run_mode='background';
