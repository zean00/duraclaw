ALTER TABLE agent_instance_versions ADD COLUMN IF NOT EXISTS profile_config jsonb NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS monitor_lease_owner text;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS monitor_lease_expires_at timestamptz;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS last_monitored_at timestamptz;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS active_pattern jsonb NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS sessions_monitor_claim_idx
ON sessions (updated_at, monitor_lease_expires_at, last_monitored_at);
