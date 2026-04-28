CREATE TABLE IF NOT EXISTS agent_instance_versions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id text NOT NULL,
    agent_instance_id text NOT NULL,
    version integer NOT NULL,
    name text NOT NULL DEFAULT '',
    model_config jsonb NOT NULL DEFAULT '{}'::jsonb,
    system_instructions text NOT NULL DEFAULT '',
    tool_config jsonb NOT NULL DEFAULT '{}'::jsonb,
    mcp_config jsonb NOT NULL DEFAULT '{}'::jsonb,
    workflow_config jsonb NOT NULL DEFAULT '{}'::jsonb,
    policy_config jsonb NOT NULL DEFAULT '{}'::jsonb,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    activated_at timestamptz,
    UNIQUE (customer_id, agent_instance_id, version),
    FOREIGN KEY (customer_id, agent_instance_id) REFERENCES agent_instances(customer_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS agent_instance_versions_instance_idx ON agent_instance_versions (customer_id, agent_instance_id, version DESC);

ALTER TABLE agent_instances ADD COLUMN IF NOT EXISTS current_version_id uuid REFERENCES agent_instance_versions(id);

ALTER TABLE runs ADD COLUMN IF NOT EXISTS agent_instance_version_id uuid REFERENCES agent_instance_versions(id);

CREATE INDEX IF NOT EXISTS runs_agent_instance_version_idx ON runs (agent_instance_version_id);
