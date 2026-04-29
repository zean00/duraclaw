ALTER TABLE scheduler_jobs ADD COLUMN IF NOT EXISTS user_id text;
ALTER TABLE scheduler_jobs ADD COLUMN IF NOT EXISTS agent_instance_id text;
ALTER TABLE scheduler_jobs ADD COLUMN IF NOT EXISTS session_id text;

UPDATE scheduler_jobs
SET user_id=COALESCE(user_id, payload->>'user_id'),
    agent_instance_id=COALESCE(agent_instance_id, payload->>'agent_instance_id'),
    session_id=COALESCE(session_id, payload->>'session_id')
WHERE user_id IS NULL OR agent_instance_id IS NULL OR session_id IS NULL;

CREATE INDEX IF NOT EXISTS scheduler_jobs_customer_user_idx ON scheduler_jobs (customer_id, user_id, next_run_at);
