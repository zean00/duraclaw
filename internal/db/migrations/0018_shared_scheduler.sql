CREATE TABLE IF NOT EXISTS shared_scheduler_jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    job_key text NOT NULL,
    title text NOT NULL DEFAULT '',
    job_type text NOT NULL DEFAULT 'eligibility_poll',
    schedule text NOT NULL,
    timezone text NOT NULL DEFAULT 'UTC',
    enabled boolean NOT NULL DEFAULT true,
    next_run_at timestamptz NOT NULL,
    external_service jsonb NOT NULL DEFAULT '{}'::jsonb,
    fanout_action text NOT NULL DEFAULT 'outbound_intent' CHECK (fanout_action IN ('outbound_intent','durable_run')),
    message_template text NOT NULL DEFAULT '',
    run_input_template jsonb NOT NULL DEFAULT '{}'::jsonb,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    last_fired_at timestamptz,
    lease_owner text,
    lease_expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (customer_id, job_key)
);

CREATE INDEX IF NOT EXISTS shared_scheduler_jobs_claim_idx ON shared_scheduler_jobs (enabled, next_run_at, lease_expires_at);

CREATE TABLE IF NOT EXISTS shared_scheduler_subscriptions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    shared_job_id uuid NOT NULL REFERENCES shared_scheduler_jobs(id) ON DELETE CASCADE,
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    user_id text NOT NULL,
    session_id text NOT NULL,
    agent_instance_id text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    subscriber_metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (shared_job_id, user_id)
);

CREATE INDEX IF NOT EXISTS shared_scheduler_subscriptions_user_idx ON shared_scheduler_subscriptions (customer_id, user_id, enabled);
CREATE INDEX IF NOT EXISTS shared_scheduler_subscriptions_job_idx ON shared_scheduler_subscriptions (shared_job_id, enabled);

CREATE TABLE IF NOT EXISTS shared_scheduler_fires (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    shared_job_id uuid NOT NULL REFERENCES shared_scheduler_jobs(id) ON DELETE CASCADE,
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    scheduled_fire_at timestamptz NOT NULL,
    status text NOT NULL DEFAULT 'succeeded',
    selected_count integer NOT NULL DEFAULT 0,
    created_count integer NOT NULL DEFAULT 0,
    error text,
    request_summary jsonb NOT NULL DEFAULT '{}'::jsonb,
    response_summary jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (shared_job_id, scheduled_fire_at)
);

CREATE TABLE IF NOT EXISTS shared_scheduler_deliveries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    shared_job_id uuid NOT NULL REFERENCES shared_scheduler_jobs(id) ON DELETE CASCADE,
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    user_id text NOT NULL,
    scheduled_fire_at timestamptz NOT NULL,
    action text NOT NULL,
    status text NOT NULL DEFAULT 'queued',
    subscriber_payloads jsonb NOT NULL DEFAULT '[]'::jsonb,
    outbound_intent_id uuid REFERENCES outbound_intents(id) ON DELETE SET NULL,
    run_id uuid REFERENCES runs(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (shared_job_id, scheduled_fire_at, user_id, action)
);

CREATE INDEX IF NOT EXISTS shared_scheduler_deliveries_run_idx ON shared_scheduler_deliveries (run_id, status);
