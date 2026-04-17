# Changelog

## v0.2 (2026-04-16)

### Added
- **Claude Code–style skills**
  - Skills discovered from per-user `data/users/<userID>/skills/<name>/SKILL.md` plus project `.claude/skills/**/SKILL.md` (compat).
  - User can invoke skills directly via `$<skill-name> ...`.
  - Model can invoke skills via the new `skill.invoke` tool (with shell injections executed inside the existing no-network sandbox).
  - Bundled `skill-creator` skill.
- **Per-user agent personality files** under `config/agents/<agent_key>/PERSONALITY.md` (auto-created with defaults; used by chat + proactive agents).
- **Canonical tool metadata** (`internal/tools.ToolSpec` + registry) powering discovery (`tool.search`) and the new `tool.describe`.
- **Tool documentation generator**: `go run ./cmd/tether-tooldocs` → `docs/tools.md`.
- `docs/skills.md` describing skills layout + frontmatter.

### Changed
- **Filesystem tools hardened**
  - `read`/`write` use `O_NOFOLLOW` to reduce symlink race risk.
  - Overwriting existing files requires `confirm.request` + `confirm_token`, **except** personality files.
  - Personality overwrites are backed up to a per-agent `.history/` directory.
  - Absolute `/work/...` paths are accepted as a safe alias for sandbox-relative paths.
- **Proactive engine** now ensures personality files exist for built-in and configured custom proactive agents.

## v0.1

- Initial release.
