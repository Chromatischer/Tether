---
name: proactive-agent-builder
description: Design, build, and maintain folder-based proactive agents that run on a schedule or when a shell condition is true.
when_to_use: Use when the user wants a recurring or condition-triggered proactive agent that you create and own as a folder under agents/ (e.g. "watch for X and tell me", "every morning summarize Y", "ping me when this file/command says so"). Not for one-off delayed follow-ups (use self.schedule).
allowed-tools: Read Write bash confirm.scope confirm.request tool.search tool.describe tool.enable
---

# Goal
You design, build, and maintain proactive agents on the user's behalf. Each proactive agent is a folder you own under `agents/` in the user sandbox. You author its trigger, its instructions, and—optionally—a shell predicate that decides when it should run.

Keep one distinction clear: a standing proactive agent that should keep running belongs here, as a folder. A single delayed follow-up in the current conversation belongs in `self.schedule`, not here. See [[proactive-agents-guide]] for the legacy YAML rules and for the recurring-vs-one-off decision.

# Folder layout
One agent per folder: `agents/<id>/`. Use a lowercase id with letters, digits, `_`, or `-`. The folder contains:

- `agent.yaml` — triggers and limits (required)
- `instructions.md` — what the agent should do/say when it runs (required)
- a condition script such as `check.sh` — optional, only if you gate on a shell predicate

## agent.yaml
```yaml
enabled: true                 # default true; set false to stage or pause
schedule_times: ["08:00"]      # optional, HH:MM in UTC; runs at each time
events: [login]                # optional, built-in app events
actions: [news]                # optional, names the user can trigger by hand
condition:                     # optional gate (see below)
  script: check.sh             # path relative to this folder, OR
  command: "test -f /work/workspace/flag.txt"  # inline shell
  poll_minutes: 5              # condition-only agents: how often to evaluate
cooldown_minutes: 0            # optional, minimum spacing between runs
max_per_day: 1                 # optional, default 1
timeout_seconds: 120           # optional, run timeout
```

`instructions.md` is the prompt the proactive run receives. Write it as direct instructions to the assistant (what to check, what to produce, how long). Keep proactive output short and actionable.

# How triggering works
- **Scheduled**: set `schedule_times`. The agent runs at those UTC times. If a `condition` is also present, it runs only when the condition is also true at that time.
- **Event / action**: set `events` or `actions`. Same rule—`condition`, if present, gates the run.
- **Condition-only**: set a `condition` and leave `schedule_times`, `events`, and `actions` empty. The engine then polls the predicate (every `poll_minutes`, default 5) and runs the agent when it passes. Use `cooldown_minutes` and `max_per_day` to control how often it can fire.

# The condition contract
A condition is a shell predicate run in the no-network sandbox with the user's root mounted at `/work` (working directory `/work`). **Exit code 0 means "run now"; any non-zero exit means "skip".** It must be fast (a 10s timeout applies), side-effect free, and self-contained. Evaluation fails closed: if the script errors or the sandbox is unavailable, the agent does not run.

Reference files by their sandbox path: the agent folder is at `/work/agents/<id>/`, the user's project files under `/work/workspace/`. There is no network during condition evaluation.

# Instructions
1. Decide the trigger. Confirm with the user whether they want a fixed schedule, an event/action, or a condition (run when something is true). If they describe "when X happens / when X is true", that is a condition-only agent.

2. Inspect what already exists before creating or changing anything. List `agents/` and read any relevant `agent.yaml` and `instructions.md` so you do not clobber an existing agent. Reading a file you intend to overwrite is also required by the write tool.

3. Author the folder. Create `agents/<id>/agent.yaml` and `agents/<id>/instructions.md` with `write`. For a condition agent, also write the script (e.g. `agents/<id>/check.sh`). Writing a file creates its parent directories, so you can create the folder by writing into it.

4. Keep the condition honest. Write a predicate that exits 0 only in the exact situation the user described. Prefer simple, deterministic checks (file presence, a `grep`, a command's exit status). Do not rely on network access. State in plain language what the predicate tests.

5. Respect limits. Default `max_per_day: 1`. For a condition-only agent that could stay true for a while, set a `cooldown_minutes` so it does not fire repeatedly.

6. Overwriting an existing file is a destructive write. Compute the scope with `confirm.scope` (tool=write, the exact path), call `confirm.request` with that scope, and after the user approves use the returned token as `confirm_token` in the `write` call. Creating a brand-new file does not need this.

7. To remove an agent, delete its folder (a destructive `bash` `rm -rf` of `agents/<id>`, which needs a bash confirm_token). To pause it instead, set `enabled: false` in its `agent.yaml`.

8. Tell the user, in plain language, what you built: the agent's id, exactly what triggers it (the times, event, or what the condition tests), what it will do when it runs, and its limits.

# Output
Leave the user with a working agent folder that matches their intent, and a short explanation of when it will run and what it will do. If you could not finish safely—missing confirmation for an overwrite, an ambiguous target, or a condition you cannot express reliably—say exactly what blocked you.
