CREATE TABLE IF NOT EXISTS user_runtime_limits (
    customer_id text NOT NULL,
    user_id text NOT NULL,
    max_daily_tokens integer,
    max_weekly_tokens integer,
    max_monthly_tokens integer,
    max_daily_model_cost_micros bigint,
    max_weekly_model_cost_micros bigint,
    max_monthly_model_cost_micros bigint,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, user_id),
    FOREIGN KEY (customer_id, user_id) REFERENCES users(customer_id, id) ON DELETE CASCADE
);

ALTER TABLE model_usage_ledger
    ADD COLUMN IF NOT EXISTS user_id text NOT NULL DEFAULT '';

UPDATE model_usage_ledger l
SET user_id = r.user_id
FROM runs r
WHERE l.run_id = r.id
AND l.user_id = '';

CREATE INDEX IF NOT EXISTS model_usage_ledger_customer_user_period_idx
    ON model_usage_ledger (customer_id, user_id, created_at);
