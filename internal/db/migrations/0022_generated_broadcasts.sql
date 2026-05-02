ALTER TABLE broadcasts ADD COLUMN IF NOT EXISTS generation_mode text NOT NULL DEFAULT 'direct';
ALTER TABLE broadcasts ADD COLUMN IF NOT EXISTS agent_instance_id text;
ALTER TABLE broadcasts ADD COLUMN IF NOT EXISTS generation_request jsonb NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE broadcast_targets ADD COLUMN IF NOT EXISTS generation_run_id uuid REFERENCES runs(id) ON DELETE SET NULL;
ALTER TABLE broadcast_targets ADD COLUMN IF NOT EXISTS last_error text;

ALTER TABLE broadcast_targets DROP CONSTRAINT IF EXISTS broadcast_targets_status_check;
ALTER TABLE broadcasts DROP CONSTRAINT IF EXISTS broadcasts_status_check;

ALTER TABLE broadcast_targets
    ADD CONSTRAINT broadcast_targets_status_check
    CHECK (status IN ('pending','generating','processing','queued','sent_to_nexus','delivered','failed','generation_failed','cancelled'));

ALTER TABLE broadcasts
    ADD CONSTRAINT broadcasts_status_check
    CHECK (status IN ('draft','queued','sent_to_nexus','delivered','failed','generation_failed','cancelled'));

CREATE INDEX IF NOT EXISTS broadcast_targets_generation_run_idx
    ON broadcast_targets (generation_run_id, status, updated_at)
    WHERE generation_run_id IS NOT NULL;
