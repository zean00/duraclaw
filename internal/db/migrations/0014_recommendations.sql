CREATE TABLE IF NOT EXISTS recommendation_items (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    kind text NOT NULL DEFAULT 'activity',
    title text NOT NULL,
    description text NOT NULL DEFAULT '',
    tags jsonb NOT NULL DEFAULT '[]'::jsonb,
    url text NOT NULL DEFAULT '',
    priority integer NOT NULL DEFAULT 0,
    sponsored boolean NOT NULL DEFAULT false,
    sponsor_name text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','paused','archived')),
    valid_from timestamptz,
    valid_until timestamptz,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS recommendation_items_customer_status_idx ON recommendation_items (customer_id, status, priority DESC, created_at DESC);

CREATE TABLE IF NOT EXISTS recommendation_decisions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    run_id uuid REFERENCES runs(id) ON DELETE SET NULL,
    user_id text NOT NULL DEFAULT '',
    session_id text NOT NULL DEFAULT '',
    scope_intent text NOT NULL DEFAULT '',
    context_mode text NOT NULL DEFAULT '',
    candidate_item_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
    selected_item_id uuid REFERENCES recommendation_items(id) ON DELETE SET NULL,
    recommendation_text text NOT NULL DEFAULT '',
    reason text NOT NULL DEFAULT '',
    delivery_status text NOT NULL DEFAULT 'none',
    error text NOT NULL DEFAULT '',
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS recommendation_decisions_customer_run_idx ON recommendation_decisions (customer_id, run_id, created_at DESC);

CREATE TABLE IF NOT EXISTS recommendation_jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    run_id uuid REFERENCES runs(id) ON DELETE SET NULL,
    user_id text NOT NULL,
    session_id text NOT NULL,
    scope_intent text NOT NULL DEFAULT '',
    context_mode text NOT NULL DEFAULT '',
    recommendation_context text NOT NULL DEFAULT '',
    original_response text NOT NULL DEFAULT '',
    config jsonb NOT NULL DEFAULT '{}'::jsonb,
    candidate_item_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','leased','sent','no_candidate','failed','cancelled')),
    lease_owner text NOT NULL DEFAULT '',
    lease_expires_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS recommendation_jobs_claim_idx ON recommendation_jobs (status, lease_expires_at, created_at);
CREATE INDEX IF NOT EXISTS recommendation_jobs_customer_status_idx ON recommendation_jobs (customer_id, status, created_at DESC);
