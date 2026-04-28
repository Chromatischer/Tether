# Changelog

## v0.6 (2026-04-28)

### Added
- **Linux installer and updater**
  - Added root-oriented Linux install and update scripts.
  - Installs Tether binaries, systemd service files, runtime directories, and narrow sudo rules for in-app updates.
- **In-app admin updates**
  - Admins can check, run, and configure application updates from Tether.
  - Supports latest GitHub release or latest commit from a selected branch.
  - Auto-updates can be scheduled at a configured UTC time.

### Changed
- **Database migration baselines**
  - Collapsed historical migrations into the v0.6 baseline snapshot.
  - Added a generic release baseline guard so future versions can keep compact migration sets.
- **Repository housekeeping**
  - Added local agent/tooling ignores and removed project references to the ignored documentation tree.

## v0.5 (2026-04-28)

### Added
- **OpenRouter streamed error handling**
  - Streaming responses now surface top-level OpenRouter stream errors and reason fallbacks.

### Changed
- **Tool-call streaming now preserves large function arguments**
  - OpenRouter `/responses` streaming now accumulates `response.function_call_arguments.delta` chunks and merges reconstructed arguments back into final `function_call` items before execution.
  - Fixes sessions where short tool calls worked, but larger `bash`/`write` calls degraded into `invalid tool arguments JSON` failures once the model started emitting long code payloads.
- **Removed app-level LLM output caps from agent prompt paths**
  - The tool-calling loop no longer sets `max_output_tokens`, avoiding truncation of long tool arguments.
  - Standalone prompt helpers and conversation summarization also no longer impose fixed `MaxOutputTokens` caps.
- **TUI agent streaming uses an inactivity watchdog instead of a whole-turn deadline**
  - Active streams stay alive as long as tokens or tool events continue arriving.
  - Silent/stalled streams are canceled after an idle window instead of blocking the chat queue indefinitely.
- **Responses continuation preserves reasoning items**
  - Replayable response items now keep provider reasoning payloads needed for continuation.

## v0.4 (2026-04-22)

### Added
- **Templated, per-user system prompts**
  - New `internal/systemprompt` package renders chat and proactive system prompts from templates with `{{username}}`, `{{user_id}}`, `{{conversation_id}}`, `{{session_id}}`, and `{{mode}}` placeholders.
  - Users can override the built-in templates by editing `config/prompts/chat_system.md` or `config/prompts/proactive_system.md` in their sandbox; missing files are auto-seeded with defaults.
  - Replaces the two hardcoded prompt string constants that previously lived in `internal/agent/agent.go`.
- **Session status and attached-context introspection**
  - New `agent.SessionStatus` surface exposes session age, idle time, per-turn and cumulative token counts, cost, last model, and context-window percentage.
  - New `AttachedContextStatus` reports which pieces (personality, summary, history, memory, skills index, invoked skills) are currently attached and an estimated token footprint.
  - Rendered in both the TUI and Discord gateway via dedicated status renderers.
- **Persisted LLM usage tracking**
  - New `internal/store/llm_usage.go` reads `audit_events` of type `llm_usage` to produce per-conversation and cumulative token/cost rollups.
  - Usage is used to drive the new status views and the context-percentage indicator.
- **Model catalog and admin model picker**
  - OpenRouter client now fetches the model catalog (`/models`) and per-model endpoint metadata.
  - Admin setup screen gained an interactive model/endpoint picker with search and per-endpoint pricing/context info.
- **Incremental conversation summary checkpoints**
  - New migration `0017_conversation_summary_checkpoint.sql` adds `summarized_through_message_id` to `conversation_summaries`.
  - Store helpers now expose `GetConversationSummaryState` / `UpsertConversationSummaryWithCheckpoint` so the agent can summarize only messages past the checkpoint instead of re-summarizing the whole tail.
- **User-space prompt file helpers** in `internal/userspace` for resolving and ensuring system prompt template files under each user's sandbox.

### Changed
- **Adaptive conversation compaction replaces fixed thresholds**
  - Summarization is now driven by the active model's context length (fetched via the new model catalog) instead of the old "40 messages / 30 minutes" rule.
  - The agent compacts when estimated input exceeds ~66% of the model's context and keeps a ~22% raw tail budget for recent turns.
  - Summary prompts now also ask for unresolved blockers and allow up to 250 words / 450 output tokens.
- **`ReplyStream` builds context lazily from the store**
  - Callers no longer pre-fetch the last 25 messages; `buildContextInputItemsWithSession` now pulls history itself, respecting the summary checkpoint.
  - Session activity is touched before forking so idle/age accounting stays accurate across streamed replies.
- **Proactive prompts gained a per-user variant** (`RunProactivePromptForUser`) so proactive runs pick up the user's prompt template overrides and personality.
- **Sandbox and tool-call plumbing hardened** alongside additional tests in `bash_tool_test.go`, `bwrap_test.go`, `toolcalling_test.go`, `admin_env_test.go`, `app_test.go`, `chat_test.go`, and new `status_test.go` / `systemprompt_test.go` / `prompt_files_test.go` suites.
- **MCP manager, proactive engine, and self-schedule runner** updated to flow through the new per-user prompt and status paths.

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
- **Autonomy and product vision notes**
  - Added the long-horizon product direction.
  - Added Tether’s autonomy ladder and confirmation policy.
- **`confirm.scope` tool** to compute the exact confirmation scope string needed before destructive tool calls.

### Changed
- **File editing is less permission-token driven and more context-bound**
  - `write` no longer requires a confirmation token by default for overwriting existing files.
  - Overwriting an existing file now requires that the same agent session has already read that exact path first.
  - Creating a brand-new file still works without a prior read.
  - Personality file overwrites still create `.history` backups, but now follow the same read-before-edit rule.
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
- **Tool documentation generator** for the tool reference.
- Added skills layout and frontmatter notes.

### Changed
- **Filesystem tools hardened**
  - `read`/`write` use `O_NOFOLLOW` to reduce symlink race risk.
  - Overwriting existing files requires `confirm.request` + `confirm_token`, **except** personality files.
  - Personality overwrites are backed up to a per-agent `.history/` directory.
  - Absolute `/work/...` paths are accepted as a safe alias for sandbox-relative paths.
- **Proactive engine** now ensures personality files exist for built-in and configured custom proactive agents.

## v0.1

- Initial release.
