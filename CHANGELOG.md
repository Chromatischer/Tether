# Changelog

## v0.3 (2026-04-19)

### Added
- **Constrained subagents**
  - `subagent.spawn` now accepts a caller-selected `allowed_tools` list and optional one-time skill preload.
  - Subagents run in dedicated sessions, stream progress history, and expose richer `subagent.status` output.
  - Added tests for subagent manager behavior and tool validation.
- **Bundled `proactive-agents-guide` skill** for creating, editing, and explaining recurring proactive agents vs. one-off `self.schedule` runs.
- **TUI rich-text rendering** for assistant/system output, including headings, lists, code fences, links, rules, and Markdown tables.
- **Chat composer autocomplete** for slash commands and user-invocable skills.

### Changed
- **Conversation summaries hardened**
  - Stored summaries are now re-injected as untrusted reference context instead of system instructions.
  - Summary generation explicitly excludes assistant instructions, prompt injection attempts, and credential/token requests.
  - Proactive prompts now treat conversation summaries as untrusted reference text too.
- **Subagent safety tightened**
  - Subagents cannot spawn additional subagents or invoke more skills after launch.
  - Subagent status lookup is now scoped per user.
  - Regular subagent runs no longer inherit the full skill index in context.
- **Tool call streaming and chat UX improved**
  - Tool events now include truncated result previews so the TUI can merge call + result into one widget.
  - System notices render without the old `notice` label, and assistant output uses rich-text formatting.
  - The composer now uses explicit input/suggestion/send focus states with Tab navigation and Enter-to-apply/send behavior.
- **Confirmation flow docs updated** to describe host-paused confirmation and automatic resume behavior for destructive tool actions.

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
