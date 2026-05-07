ALTER TABLE memories ADD COLUMN IF NOT EXISTS last_used_at timestamptz;
ALTER TABLE memories ADD COLUMN IF NOT EXISTS usage_count integer NOT NULL DEFAULT 0;

ALTER TABLE preferences ADD COLUMN IF NOT EXISTS last_used_at timestamptz;
ALTER TABLE preferences ADD COLUMN IF NOT EXISTS usage_count integer NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS memories_customer_user_usage_idx
    ON memories (customer_id, user_id, usage_count DESC, updated_at DESC);

CREATE INDEX IF NOT EXISTS preferences_customer_user_usage_idx
    ON preferences (customer_id, user_id, usage_count DESC, updated_at DESC);
