ALTER TABLE async_outbox ADD COLUMN IF NOT EXISTS claim_expires_at timestamptz;

UPDATE async_outbox
SET claim_expires_at=claimed_at + interval '5 minutes'
WHERE completed_at IS NULL
  AND claimed_at IS NOT NULL
  AND claim_expires_at IS NULL;

CREATE INDEX IF NOT EXISTS async_outbox_claim_lease_idx
    ON async_outbox (completed_at, available_at, claim_expires_at);
