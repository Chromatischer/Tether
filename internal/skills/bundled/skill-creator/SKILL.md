---
name: skill-creator
description: Create a new Claude Code–style skill under Tether’s per-user skills directory (skills/<name>/SKILL.md).
disable-model-invocation: true
argument-hint: "<skill-name>"
---

Create a new Claude Code–style skill for **Tether**.

Important: In Tether, user skills live under the per-user directory:
- `skills/<skill-name>/SKILL.md`

Do **not** create user skills under `.claude/skills/` unless the user explicitly asks for a project-scoped skill.

## Inputs
- Skill name: `$0` (required). Use lowercase letters, numbers, and hyphens.

## Steps
1) Validate the skill name (lowercase, hyphens).
2) Create the directory: `skills/<skill-name>/`
3) Create `skills/<skill-name>/SKILL.md` with YAML frontmatter:
   - `name`
   - `description` (front-load trigger phrases users will say)
   - `when_to_use` (example requests)
   - If this workflow has side effects, set `disable-model-invocation: true`
4) If useful, add supporting files next to SKILL.md:
   - `template.md`
   - `examples/…`
   - `scripts/…`
5) Ensure SKILL.md links to supporting files with relative markdown links.

## Output
- Print created paths and a usage example like: `$<skill-name> ...`
