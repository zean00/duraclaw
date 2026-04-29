ALTER TABLE customer_runtime_limits
    ADD COLUMN IF NOT EXISTS max_daily_tokens integer,
    ADD COLUMN IF NOT EXISTS max_weekly_tokens integer,
    ADD COLUMN IF NOT EXISTS max_monthly_tokens integer,
    ADD COLUMN IF NOT EXISTS max_daily_model_cost_micros bigint,
    ADD COLUMN IF NOT EXISTS max_weekly_model_cost_micros bigint,
    ADD COLUMN IF NOT EXISTS max_monthly_model_cost_micros bigint;

ALTER TABLE agent_instance_runtime_limits
    ADD COLUMN IF NOT EXISTS max_daily_tokens integer,
    ADD COLUMN IF NOT EXISTS max_weekly_tokens integer,
    ADD COLUMN IF NOT EXISTS max_monthly_tokens integer,
    ADD COLUMN IF NOT EXISTS max_daily_model_cost_micros bigint,
    ADD COLUMN IF NOT EXISTS max_weekly_model_cost_micros bigint,
    ADD COLUMN IF NOT EXISTS max_monthly_model_cost_micros bigint;

CREATE TABLE IF NOT EXISTS model_usage_ledger (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    agent_instance_id text NOT NULL,
    run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    model_call_id uuid NOT NULL REFERENCES model_calls(id) ON DELETE CASCADE,
    provider text NOT NULL,
    model text NOT NULL,
    input_tokens integer NOT NULL DEFAULT 0,
    output_tokens integer NOT NULL DEFAULT 0,
    total_tokens integer NOT NULL DEFAULT 0,
    cost_micros bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (model_call_id)
);

CREATE INDEX IF NOT EXISTS model_usage_ledger_customer_period_idx
    ON model_usage_ledger (customer_id, agent_instance_id, created_at);
