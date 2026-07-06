---
name: proactive-agents-guide
description: Explain and route proactive work correctly across built-in rules, folder-based custom agents, and one-off self.schedule follow-ups.
when_to_use: Use when the user asks to add, edit, remove, inspect, or explain proactive behavior, proactive schedules, proactive actions, recurring proactive agents, or one-time follow-ups.
allowed-tools: Read Write confirm.scope confirm.request tool.search tool.describe tool.enable
---

# Goal
Help the user manage their proactive setup, and route each request to the right mechanism. There are three, and keeping them straight is the whole job:

1. **Built-in proactive rules** — `daily_brief`, `open_loops`, and `inactivity`. These live in `config/proactive.yaml` in the user sandbox and are edited as YAML.
2. **Custom proactive agents** — standing agents the assistant owns, each a folder under `agents/`. These are NOT in `proactive.yaml`. To create or change one, use the [[proactive-agent-builder]] skill.
3. **One-off delayed follow-ups** — a single later run in the current conversation, via `self.schedule`. Not a standing agent.

Do not use `self.schedule` for something recurring. Do not edit `proactive.yaml` to add a custom agent — custom agents are folders. Legacy YAML `agents:` is no longer supported; an `agents:` block in `proactive.yaml` is ignored.

# Inputs
- `$ARGUMENTS`: what the user wants changed or explained

# Instructions
1. Decide which of the three mechanisms applies.

   Recurring behavior at fixed times, on app events, on manual action, or gated by a condition that should be checked repeatedly → a custom proactive agent. Hand this to [[proactive-agent-builder]].

   Changes to the daily brief, open-loops review, or inactivity nudge → edit `config/proactive.yaml` (built-in rules).

   Something that should happen once, later, in this conversation → `self.schedule`.

2. When the request is about a custom proactive agent, defer to [[proactive-agent-builder]] rather than editing YAML. Custom agents are folders under `agents/`, each with `agent.yaml`, `instructions.md`, and an optional condition script.

3. When explaining the current setup, inspect it first and describe it in plain language.

   Read `config/proactive.yaml` for the built-in rules. List `agents/` and read each agent's `agent.yaml`/`instructions.md` for the custom agents. Explain built-in behavior separately from custom agents.

4. When editing built-in rules in `config/proactive.yaml`, change only what the request needs and preserve everything else.

   Built-in keys: `daily_brief` (`enabled`, `time`), `open_loops` (`enabled`, `time`), `inactivity` (`enabled`, `minutes`). Times are `HH:MM` in UTC.

5. Overwriting `config/proactive.yaml` is a destructive write. Compute the scope with `confirm.scope` (tool=write, path=config/proactive.yaml), call `confirm.request` with that scope, and after the user approves use the returned token as `confirm_token` in the `write` call. The write tool writes complete file content; it does not patch in place.

6. For a one-time follow-up, use `self.schedule`. If it is not enabled, discover and enable it through the tool system first. Explain that it is a one-off delayed run, not a standing proactive agent.

7. Keep the user informed in human terms: what you changed, what will trigger it, and what it will do. If you turned a built-in rule off instead of changing its time, say so.

# Output
Give the user a clear explanation or a completed change. For custom-agent work, route to [[proactive-agent-builder]] and summarize the result. For built-in changes, leave an updated `config/proactive.yaml` and explain it. For a one-off, create it with `self.schedule` and say when it will run. If you could not finish safely, say exactly what blocked you.
