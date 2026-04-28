ALTER TABLE outbound_intents DROP CONSTRAINT IF EXISTS outbound_intents_status_check;
ALTER TABLE broadcast_targets DROP CONSTRAINT IF EXISTS broadcast_targets_status_check;
ALTER TABLE broadcasts DROP CONSTRAINT IF EXISTS broadcasts_status_check;

UPDATE outbound_intents SET status='sent_to_nexus' WHERE status='sent';
UPDATE broadcast_targets SET status='sent_to_nexus' WHERE status='sent';
UPDATE broadcasts SET status='sent_to_nexus' WHERE status='sent';

ALTER TABLE outbound_intents
    ADD CONSTRAINT outbound_intents_status_check
    CHECK (status IN ('pending','queued','sent_to_nexus','delivered','failed','cancelled'));

ALTER TABLE broadcast_targets
    ADD CONSTRAINT broadcast_targets_status_check
    CHECK (status IN ('pending','queued','sent_to_nexus','delivered','failed','cancelled'));

ALTER TABLE broadcasts
    ADD CONSTRAINT broadcasts_status_check
    CHECK (status IN ('draft','queued','sent_to_nexus','delivered','failed','cancelled'));
