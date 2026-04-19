---
name: proactive-agents-guide
description: Create, modify, remove, and explain proactive agents, recurring proactive behavior, and self.schedule correctly.
when_to_use: Use when the user asks you to add, edit, remove, inspect, or explain proactive agents, recurring proactive behavior, proactive schedules, proactive actions, or self.schedule.
allowed-tools: Read Write confirm.request tool.search tool.describe tool.enable
---

# Goal
Help the agent manage the user's proactive setup correctly.

When using this skill, keep one distinction clear at all times. A recurring proactive agent belongs in the proactive rules. A one-time delayed follow-up belongs in `self.schedule`. Do not use `self.schedule` when the user is asking for an ongoing recurring proactive agent. Do not describe a recurring proactive rule as if it were just a delayed follow-up.

# Inputs
- `$ARGUMENTS`: what the user wants changed or explained

Typical requests include creating a new proactive agent, changing when one runs, changing its instructions, removing one, turning one on or off, explaining what already exists, or setting up a one-time follow-up instead of a recurring proactive rule.

# Instructions
1. First decide whether the user wants a recurring proactive rule or a one-time delayed run.

   If the user wants something that should happen every day, at fixed times, on login, on task changes, or when manually triggered by name later, treat it as a proactive agent rule.

   If the user wants something that should happen once later in the same conversation, treat it as `self.schedule` instead of a proactive agent edit.

2. If the request is about proactive agents, inspect the current proactive rules before proposing or making changes.

   Read `config/proactive.yaml` from the user sandbox if it exists.

   If it does not exist, assume the user may still have proactive rules stored elsewhere by the product, so do not claim there are no rules with certainty. Say that no editable `config/proactive.yaml` file is present in the sandbox and proceed carefully.

3. When explaining the current setup, describe it in plain language.

   Explain built-in proactive behavior separately from custom agents under `agents:`.

   Built-in behavior includes things like `daily_brief`, `open_loops`, and `inactivity`.

   Custom proactive agents live under `agents:` and can use fields such as `id`, `enabled`, `instructions`, `schedule_times`, `events`, `actions`, `cooldown_minutes`, `max_per_day`, and `timeout_seconds`.

4. When creating a new recurring proactive agent, add a new entry under `agents:`.

   Use a simple lowercase id with letters, numbers, underscores, or hyphens.

   Include `enabled: true` unless the user asked to stage it disabled.

   Put the user's desired behavior into `instructions`.

   Use `schedule_times` for recurring times in `HH:MM`.

   Use `events` only when the user asked for event-driven behavior such as login or task changes.

   Use `actions` when the user wants to be able to trigger it intentionally by name.

   Add `cooldown_minutes`, `max_per_day`, or `timeout_seconds` only when they are useful or requested.

5. When modifying an existing proactive agent, preserve everything unrelated to the request.

   Change only the fields needed for the requested outcome.

   Do not silently remove other schedules, events, actions, or instructions unless the user asked for that.

   If multiple agents could match the request and the intended target is unclear, say so clearly instead of making a risky guess.

6. When removing or disabling a proactive agent, prefer the safer option if the user was ambiguous.

   If the user says they want it gone, remove the agent entry.

   If the user says they want it paused or stopped for now, set `enabled: false`.

7. When you need to overwrite `config/proactive.yaml`, handle it as a destructive write.

   Use `confirm.request` first so the user can approve the overwrite.

   Then write the full updated file content with `write`, including the `confirm_token`.

   Do not pretend you can patch the file in place. The write tool writes complete file content.

8. Keep the user informed about what you are changing in human terms.

   Say what agent you are creating or modifying, what will trigger it, and what it will do.

   If you are editing a schedule, mention the exact new `HH:MM` values you wrote.

   If you are turning a rule off instead of deleting it, say that explicitly.

9. If the request is really a one-time follow-up, use `self.schedule` rather than editing proactive rules.

   If `self.schedule` is not currently enabled, discover and enable it through the tool system before using it.

   Explain that this is a one-off delayed run, not a standing proactive agent.

10. Do not drift into product-internals unless the user asks.

   Mention implementation limitations only when they matter to the task, such as the need for overwrite confirmation or the fact that recurring proactive behavior belongs in the rules rather than `self.schedule`.

# Output
If the task is explanatory, give the user a clear explanation of what proactive agents they have or how the system should be configured.

If the task is a recurring proactive change, leave the user with an updated `config/proactive.yaml` that matches the request and explain the result in plain language.

If the task is a one-time delayed follow-up, create it with `self.schedule` and explain when it will run.

If you could not safely complete the change, say exactly what blocked it, such as missing file context, ambiguous agent targeting, or missing overwrite confirmation.
