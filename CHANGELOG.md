# Changelog

## v0.3 (2026-04-19)

### Added
- **Constrained subagents**
  - `subagent.spawn` now accepts a caller-selected `allowed_tools` list and optional one-time skill preload.
  - Subagents run in dedicated sessions, stream progress history, and expose richer `subagent.status` output.
  - Added tests for subagent manager behavior and tool validation.
- **MCP tool integration**
  - Added config-driven MCP server definitions with per-user enabled server lists.
  - Discovered MCP tools are now registered dynamically as `mcp.<server>.<tool>`.
  - Added MCP tool execution plumbing with confirmation-aware safety behavior.
- **Bundled `proactive-agents-guide` skill** for creating, editing, and explaining recurring proactive agents vs. one-off `self.schedule` runs.
- **Bundled `onboarding` skill** for capturing stable user preferences, writing `config/agents/chat/PERSONALITY.md`, and storing a small set of high-value memory items.
- **TUI rich-text rendering** for assistant/system output, including headings, lists, code fences, links, rules, and Markdown tables.
- **Chat composer autocomplete** for slash commands and user-invocable skills.
- **Autonomy and product vision docs**
  - Added `docs/VISION.md` for the long-horizon product direction.
  - Added `docs/AUTONOMY.md` defining Tether’s autonomy ladder and confirmation policy.
- **`confirm.scope` tool** to compute the exact confirmation scope string needed before destructive tool calls.

### Changed
- **Discord and TUI reply flow got dramatically better**
  - Discord assistant messages can now attach `1️⃣` through `🔟` reactions for numbered multiple-choice prompts, and clicking one resumes the same agent session as if the user had replied with that number.
  - The main prompt now tells Tether to format multiple-choice questions as numbered lists ending with `Reply with just the number.` so Discord reactions and TUI numeric replies share one clean interaction model.
  - The mixed-turn chat transcript now preserves time order across reasoning, tool calls, partial text, and final responses instead of collapsing them into a misleading merged block.
  - Completed reasoning and tool-call rows collapse by default, can be expanded by click, and keep live tool rows populated instead of leaving stale `running...` entries behind.
  - The working indicator came back as a bottom-pinned animated three-dot row while an agent turn is still active.
  - These interaction changes are, in practice, **really fucking good**.
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
- **Prompt and trust model tightened**
  - Main and proactive prompts now explicitly frame Tether as operating on real users, real data, and real consequences.
  - Added a stronger autonomy ladder, trust framing, and stricter guidance around confirmation for high-blast-radius actions.
- **Confirmation flow docs updated** to describe host-paused confirmation and automatic resume behavior for destructive tool actions.
- **Confirmation flow behavior clarified**
  - `/confirm` now distinguishes paused tool execution from standalone confirmation tokens in both the TUI and Discord gateway.
  - `confirm.request` now resumes cleanly with a confirmed token and preserves user-facing reason text.
- **TUI layout and visual polish**
  - Proactive notifications are stored as system messages.
  - Header, auth, chat composer, settings, and admin views now use more consistent full-width layout and background handling.

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
