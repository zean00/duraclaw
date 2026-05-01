CREATE TABLE IF NOT EXISTS tool_access_rules (
    customer_id text NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    agent_instance_id text NOT NULL,
    user_id text NOT NULL DEFAULT '',
    allowed_tools jsonb NOT NULL DEFAULT '[]'::jsonb,
    denied_tools jsonb NOT NULL DEFAULT '[]'::jsonb,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, agent_instance_id, user_id),
    FOREIGN KEY (customer_id, agent_instance_id) REFERENCES agent_instances(customer_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS tool_access_rules_customer_agent_idx
    ON tool_access_rules (customer_id, agent_instance_id, user_id);
