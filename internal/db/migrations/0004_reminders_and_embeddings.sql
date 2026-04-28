ALTER TABLE scheduler_jobs ADD COLUMN IF NOT EXISTS job_type text NOT NULL DEFAULT 'cron';
ALTER TABLE scheduler_jobs ADD COLUMN IF NOT EXISTS metadata jsonb NOT NULL DEFAULT '{}'::jsonb;

CREATE TABLE IF NOT EXISTS reminder_subscriptions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    user_id text NOT NULL,
    session_id text NOT NULL,
    agent_instance_id text NOT NULL,
    title text NOT NULL DEFAULT '',
    schedule text NOT NULL,
    timezone text NOT NULL DEFAULT 'UTC',
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    enabled boolean NOT NULL DEFAULT true,
    next_run_at timestamptz NOT NULL,
    last_fired_at timestamptz,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS reminder_subscriptions_due_idx ON reminder_subscriptions (enabled, next_run_at);
CREATE INDEX IF NOT EXISTS reminder_subscriptions_customer_user_idx ON reminder_subscriptions (customer_id, user_id, enabled);
