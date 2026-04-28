CREATE TABLE IF NOT EXISTS preferences (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    user_id text NOT NULL,
    session_id text,
    category text NOT NULL DEFAULT 'general',
    content text NOT NULL,
    condition jsonb NOT NULL DEFAULT '{}'::jsonb,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    embedding vector(768),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS preferences_customer_user_idx ON preferences (customer_id, user_id, category, updated_at);
