# Skills (Claude Code style)

Tether supports **Claude Code–style skills**: each skill is a directory containing a required `SKILL.md` file (plus optional supporting files).

## Locations

### Per-user skills (Tether default)

Stored under the per-user data root:

- `data/users/<userID>/skills/<skill-name>/SKILL.md`

This is the **primary** skill location in Tether.

### Project skills (Claude Code compatibility)

If your workspace contains Claude Code skills, Tether will also discover:

- `workspace/**/.claude/skills/<skill-name>/SKILL.md`

This is useful for repos that already ship `.claude/skills/`.

## File layout

```
my-skill/
├── SKILL.md
├── template.md
├── examples/
│   └── sample.md
└── scripts/
    └── helper.sh
```

## Frontmatter

`SKILL.md` may start with YAML frontmatter:

```yaml
---
name: my-skill
description: What this skill does
when_to_use: Example triggers
disable-model-invocation: true
allowed-tools: Bash Read
---
```

## Invoking skills

- User: type `$<skill-name> [args]` in chat.
- Model: the assistant can call the tool `skill.invoke`.

Invoked skill bodies are kept in memory and re-attached to the prompt each turn.

## Shell injection

Skills may contain shell injection placeholders (inline or fenced forms). Tether executes these in the existing **no-network sandbox** before sending the skill content to the model.

If an injection command looks destructive (rm/mv/chmod/etc.), Tether requires a confirmation token via `confirm.request`.
