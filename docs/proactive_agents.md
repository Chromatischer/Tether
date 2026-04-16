# Custom proactive agents

Tether supports custom proactive agents configured per-user in the **proactive rules YAML**.

You can edit the YAML in the TUI under **Settings → proactive rules**.

## Schema (v0.1)

```yaml
# Existing built-ins
daily_brief:
  enabled: true
  time: "08:00"   # HH:MM (UTC)
open_loops:
  enabled: true
  time: "20:00"   # HH:MM (UTC)
inactivity:
  enabled: true
  minutes: 240

# Custom agents
agents:
  - id: morning_planner
    enabled: true
    instructions: |
      Write my morning plan.
      Include: top 3 priorities, 1 quick win, and 1 risk to watch.
    schedule_times: ["07:30"]
    events: ["login", "task_changed"]
    actions: ["plan"]
    cooldown_minutes: 30
    max_per_day: 3
    timeout_seconds: 120
```

### Fields

- `agents[].id`
  - Required.
  - Must match: `[a-z0-9_-]+` (lowercase recommended).
- `agents[].instructions`
  - Your custom instruction set/prompt.
- `agents[].schedule_times`
  - List of `HH:MM` times **in UTC**.
- `agents[].events`
  - Built-in events:
    - `login`
    - `user_message`
    - `task_changed`
- `agents[].actions`
  - Custom action names you can trigger manually.

## Triggering custom actions

From the SSH TUI chat:

- `/proactive action <name>`
- `/proactive agent <id>`

From the LLM tool surface:

- `proactive.run {"action":"plan"}`
- `proactive.run {"agent_id":"morning_planner"}`
