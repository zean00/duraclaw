ALTER TABLE runs ADD COLUMN IF NOT EXISTS refinement_parent_run_id uuid REFERENCES runs(id) ON DELETE SET NULL;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS refinement_depth integer NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS suppress_direct_outbound boolean NOT NULL DEFAULT false;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS interrupt_window_started_at timestamptz;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS suppressed_response jsonb NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS runs_refinement_parent_idx ON runs (refinement_parent_run_id);
CREATE INDEX IF NOT EXISTS runs_interrupt_window_idx ON runs (customer_id, session_id, interrupt_window_started_at)
WHERE state IN ('leased','running','running_workflow','awaiting_user');

CREATE TABLE IF NOT EXISTS deferred_run_messages (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id text NOT NULL,
    user_id text NOT NULL,
    agent_instance_id text NOT NULL,
    session_id text NOT NULL,
    active_run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    message_id uuid REFERENCES messages(id) ON DELETE SET NULL,
    request_id text NOT NULL,
    idempotency_key text NOT NULL,
    input jsonb NOT NULL DEFAULT '{}'::jsonb,
    state text NOT NULL CHECK (state IN ('deferred','claimed','queued_followup')) DEFAULT 'deferred',
    created_at timestamptz NOT NULL DEFAULT now(),
    claimed_at timestamptz,
    UNIQUE (customer_id, session_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS deferred_run_messages_active_idx
ON deferred_run_messages (active_run_id, state, created_at);
