ALTER TABLE broadcasts ADD COLUMN IF NOT EXISTS external_broadcast_id text;

CREATE UNIQUE INDEX IF NOT EXISTS broadcasts_customer_external_id_idx
    ON broadcasts (customer_id, external_broadcast_id)
    WHERE external_broadcast_id IS NOT NULL AND external_broadcast_id <> '';

ALTER TABLE broadcast_targets DROP CONSTRAINT IF EXISTS broadcast_targets_status_check;
ALTER TABLE broadcasts DROP CONSTRAINT IF EXISTS broadcasts_status_check;

ALTER TABLE broadcast_targets
    ADD CONSTRAINT broadcast_targets_status_check
    CHECK (status IN ('pending','generating','processing','queued','sent_to_nexus','delivered','failed','generation_failed','cancelled','channel_suppressed'));

ALTER TABLE broadcasts
    ADD CONSTRAINT broadcasts_status_check
    CHECK (status IN ('draft','queued','sent_to_nexus','delivered','failed','generation_failed','cancelled','channel_suppressed'));
