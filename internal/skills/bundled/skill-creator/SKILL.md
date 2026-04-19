---
name: skill-creator
description: Create and maintain Tether (Claude Code–style) skills; includes Tether-specific internals (locations, discovery, substitutions, shell injection).
when_to_use: Use when the user asks to create a new skill, modify a skill, or wants to understand how skills work in Tether.
argument-hint: "<skill-name> [what it should do]"
allowed-tools: Write Read Bash
---

You are authoring **Claude Code–style skills** for **Tether**.

This skill is both a **how-to** and a **reference** for how Tether implements skills internally.

## What a “skill” is in Tether
A skill is a **directory** containing a required `SKILL.md` file (plus optional supporting files). Skills are loaded into the session by `skill.invoke`, and the invoked skill content is re-attached to the model context on later turns.

### Where skills live (paths you should write)
Inside the Tether sandbox/user root, use these relative paths:

- **Per-user skills (Tether default):** `skills/<skill-name>/SKILL.md`
- **Project skills (Claude Code compatibility):** `workspace/**/.claude/skills/<skill-name>/SKILL.md`

Notes:
- Per-user skills are preferred in Tether.
- Project skills exist for compatibility with repos that ship `.claude/skills/`.

### Discovery + precedence (important)
Tether merges skills from multiple sources with this conflict resolution order:

1) **User skills** override
2) **Project skills** override
3) **Bundled skills** (shipped with Tether)

So if you create `skills/foo/`, it will override a bundled `foo` skill.

## Skill name rules
- Lowercase letters, numbers, and hyphens only: `[a-z0-9-]`
- Max length: 64

## `SKILL.md` format
`SKILL.md` may start with YAML frontmatter:

```yaml
---
name: my-skill
description: One-line summary (should include common trigger phrases)
when_to_use: Example user requests that should trigger this skill
# disable-model-invocation: true  # hide from model + prevent skill.invoke
# user-invocable: true            # allow `$my-skill ...` (default true)
# allowed-tools: Write Read Bash  # best-effort auto-enable these tools
# model: <optional>
# effort: <optional>
# context: fork|fresh (best-effort)
# agent: <optional subagent name>
# shell: bash
# paths: ["workspace/**", "skills/**"]
---

(Body content goes here)
```

Tether-specific notes:
- If `description` is empty, Tether uses the **first paragraph of the body** as a fallback.
- `disable-model-invocation: true` prevents:
  - Listing the skill in the model-facing skills index
  - Loading it via `skill.invoke` (model invoker)
  Keep it **false/omitted** for safe, generally useful skills.
- `allowed-tools` can be a space-separated string or YAML list. Tether maps common Claude Code names to Tether tools (e.g. `Write`→`write`, `Bash`→`bash`).

## How invocation works
- **User invocation:** the user can type `$<skill-name> <args>` in chat.
- **Model invocation:** the assistant calls `skill.invoke` with `{name, arguments}`.

After invocation, the rendered skill body is stored in-memory for the session and re-attached each turn.

## Substitutions supported by Tether
During invocation, Tether applies these substitutions inside the skill body:

### Arguments
- `$ARGUMENTS` → the raw arguments string
- `$0`, `$1`, … and `$ARGUMENTS[0]`, `$ARGUMENTS[1]`, … → positional arguments (shell-quote aware parsing)

If arguments are provided but **the body does not contain** `$ARGUMENTS`, Tether appends:

```
ARGUMENTS: <your args>
```

### Session + skill dir
- `${CLAUDE_SESSION_ID}` → current skill session id
- `${CLAUDE_SKILL_DIR}` → sandbox-visible absolute path to the skill directory (e.g. `/work/skills/my-skill` or `/work/cache/bundled_skills/my-skill`)

Use `${CLAUDE_SKILL_DIR}` when referencing supporting files:

```md
See: ${CLAUDE_SKILL_DIR}/template.md
```

## Shell injection (dynamic skill rendering)
Tether supports Claude Code–style shell injection placeholders in skill bodies. These are executed **before** the skill content is returned to the model.

Important: Because this *skill-creator* skill is itself rendered by Tether, this section shows injection syntax with **escaped backticks** so the examples do not execute when you invoke `/skill-creator`. When writing a real skill, remove the backslashes.

### Inline form
Inline injection is an exclamation mark immediately followed by a backtick (shown here in escaped form): !\`command\`.

(Shown here in escaped form):

```text
Current tree: !\`ls -la workspace\`
```

### Block form
Fenced form using ```! (shown here in escaped form):

```text
\`\`\`!
cd workspace
rg -n "TODO" .
\`\`\`
```

### Execution environment
- Executed via `bash -lc` inside Tether’s existing **no-network sandbox**.
- Commands start in `/work` (the sandbox root).
  - To operate on the repo, do `cd workspace` first.
- Output is captured and substituted back into the skill body.

### Safety / confirmation
If any injected command looks destructive (e.g. contains `rm`, `mv`, `chmod`, `sed -i`, etc.), Tether requires confirmation:
- The user must run `/confirm <token>` after the assistant calls `confirm.request`.

Design guidance:
- Prefer **read-only** injections (ls/rg/cat) whenever possible.
- If destructive operations are necessary, explicitly explain what will happen and why confirmation is required.

## Bundled skills (for Tether maintainers)
Bundled skills are compiled into Tether under:

- `internal/skills/bundled/<skill-name>/SKILL.md`

At runtime, Tether extracts bundled skills into the user cache (read-only copy):

- `cache/bundled_skills/<skill-name>/...`

## Implementation checklist (when creating a new skill)
When the user asks you to create a new skill:

1) Pick a valid skill name (normalize to lowercase + hyphens).
2) Create directory: `skills/<skill-name>/`
3) Write `skills/<skill-name>/SKILL.md` with:
   - Strong `description` (include trigger phrases)
   - Concrete `when_to_use` examples
   - `allowed-tools` limited to what the skill actually needs
   - Consider `disable-model-invocation: true` only for unusually risky skills
4) Add supporting files next to `SKILL.md` if needed (e.g. `template.md`, `examples/`, `scripts/`).
5) Reference supporting files with relative links, or via `${CLAUDE_SKILL_DIR}`.
6) Test by invoking the skill (either `$<skill-name> ...` or `skill.invoke`).

## Ready-to-copy minimal template
Replace placeholders and save as `skills/<name>/SKILL.md`:

```yaml
---
name: <name>
description: <one-liner with trigger phrases>
when_to_use: <"When the user asks …"> 
allowed-tools: Write Read
---

# Goal
<What this skill helps accomplish>

# Inputs
- $ARGUMENTS: <what the args mean>

# Instructions
1) <step>
2) <step>

# Output
- <exact deliverable format>
```
