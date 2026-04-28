CREATE TABLE IF NOT EXISTS policy_packs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    version integer NOT NULL,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled','draft')),
    owner_scope text NOT NULL DEFAULT 'customer' CHECK (owner_scope IN ('global','customer','agent_instance')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (name, version)
);

CREATE TABLE IF NOT EXISTS policy_rules (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    policy_pack_id uuid NOT NULL REFERENCES policy_packs(id) ON DELETE CASCADE,
    rule_type text NOT NULL,
    enforcement_mode text NOT NULL,
    priority integer NOT NULL DEFAULT 0,
    condition jsonb NOT NULL DEFAULT '{}'::jsonb,
    action text NOT NULL DEFAULT 'allow' CHECK (action IN ('allow','deny','modify','require_clarification')),
    instruction_text text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS policy_rules_pack_mode_idx ON policy_rules (policy_pack_id, enforcement_mode, status, priority DESC);

CREATE TABLE IF NOT EXISTS policy_assignments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    policy_pack_id uuid NOT NULL REFERENCES policy_packs(id) ON DELETE CASCADE,
    customer_id text NOT NULL DEFAULT '',
    agent_instance_id text NOT NULL DEFAULT '',
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (policy_pack_id, customer_id, agent_instance_id)
);

CREATE INDEX IF NOT EXISTS policy_assignments_scope_idx ON policy_assignments (customer_id, agent_instance_id, enabled);

CREATE TABLE IF NOT EXISTS policy_evaluations (
    id bigserial PRIMARY KEY,
    run_id uuid REFERENCES runs(id) ON DELETE CASCADE,
    step_id uuid,
    workflow_run_id uuid REFERENCES workflow_runs(id) ON DELETE CASCADE,
    workflow_node_key text NOT NULL DEFAULT '',
    policy_pack_id uuid REFERENCES policy_packs(id) ON DELETE SET NULL,
    policy_rule_id uuid REFERENCES policy_rules(id) ON DELETE SET NULL,
    enforcement_mode text NOT NULL,
    decision text NOT NULL CHECK (decision IN ('allow','deny','modify','require_clarification')),
    reason text NOT NULL DEFAULT '',
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS policy_evaluations_run_idx ON policy_evaluations (run_id, created_at DESC);
CREATE INDEX IF NOT EXISTS policy_evaluations_mode_idx ON policy_evaluations (enforcement_mode, decision, created_at DESC);

ALTER TABLE tool_calls ADD COLUMN IF NOT EXISTS args_hash text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS tool_calls_nonretry_hash_idx ON tool_calls (run_id, tool_name, args_hash) WHERE retryable=false AND completed_at IS NOT NULL;
