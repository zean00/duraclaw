# Workflows

Workflows are durable, admin-defined DAGs. They are structured, versioned, permissioned, observable orchestration graphs.

Workflows are not tools. Tools are single capabilities; workflows coordinate model calls, tools, MCP calls, memory, knowledge, artifacts, timers, background jobs, and user clarification.

## Execution Model

- Workflow definitions contain nodes and edges.
- Workflow assignments enable definitions for a customer and agent instance.
- Workflow runs pin the workflow version.
- Node states and edge activations are persisted.
- Dependency-ready nodes can run concurrently.
- Merge nodes wait for active upstream parents.

## Supported Node Types

- `start`, `checkpoint`, `message`, `end`
- `split`, `merge`, `switch`, `condition`, `llm_condition`
- `tool`, `tool_call`
- `mcp`, `mcp_call`
- `mcp_list_resources`, `mcp_read_resource`, `mcp_subscribe_resource`, `mcp_unsubscribe_resource`
- `mcp_list_prompts`, `mcp_get_prompt`
- `model_call`
- `retrieve_knowledge`
- `read_memory`, `write_memory`
- `read_preference`, `write_preference`
- `read_artifact`, `process_artifact`, `write_artifact`
- `branch`, `transform`, `loop`
- `wait_timer`
- `emit_outbound_message`
- `create_background_job`
- `ask_user`

## Edge Conditions

Empty conditions always match. Deterministic edge conditions support:

```json
{"equals":{"key":"text","value":"go"}}
{"not_equals":{"key":"status","value":"failed"}}
```

## Timers and User Waits

`wait_timer` creates a one-shot scheduler wake job and pauses the workflow until the timer resumes the node.

`ask_user` persists the workflow and parent run as `awaiting_user`. Nexus resumes the same run through `POST /acp/runs/{run_id}/resume`.
