# Tether (v0.1)

Go-based personal AI assistant with:
- SSH portal (custom server) exposing a Bubble Tea TUI (alt screen + mouse)
- In-app multi-user login/signup (separate from SSH portal auth)
- SQLite persistence

## Run (dev)

Set your OpenRouter API key:

```bash
export OPENROUTER_API_KEY="..."
```

Generate a secrets master key (required for `/secret ...`):

```bash
export TETHER_MASTER_KEY="$(go run ./cmd/tether-keygen)"
```

(Optional) Signal:
- Install and register `signal-cli` for a **shared** Signal account
- Set `signal.enabled: true` and `signal.account_number` in `config/tether.yaml`

(Optional) Discord:
- Create a Discord bot and set `TETHER_DISCORD_BOT_TOKEN`
- Set `discord.enabled: true` in `config/tether.yaml`

Then:

```bash
cd /home/chromatischer/Projects/Tether

# Create a local config (not committed)
cp ./config/tether.example.yaml ./config/tether.yaml

# (Optional) enable SSH public-key auth for the portal
# cp ./config/authorized_keys.example ./config/authorized_keys

go run ./cmd/tether -config ./config/tether.yaml
```

This will start an SSH server on `:2222`.

### Connect

```bash
ssh -p 2222 <portal-user>@<host>
```

Portal auth:
- Password auth is enabled
- Public key auth is enabled if `config/authorized_keys` exists and contains your key

**Dev default password:** `tether` (change `ssh.portal_password_hash` in `config/tether.yaml`).

Generate a new bcrypt hash:

```bash
go run ./cmd/tether-passhash "your new password"
```

## Backup (dev)

```bash
go run ./cmd/tether-backup -db ./data/tether.sqlite -out ./backups
```

Also back up your `TETHER_MASTER_KEY` (and Signal data dir if you use Signal).

More details (restore notes, optional config copying): see [`docs/backup.md`](docs/backup.md).

## Status

Implemented:
- SSH server via `wish` + Bubble Tea TUI (alt screen + mouse; clickable header/buttons)
- In-app signup/login (separate from SSH portal auth)
- Chat view storing messages in SQLite
- OpenRouter client (chat/completions) + basic LLM response cache (SQLite)
- OpenRouter **tool calling loop** (OpenAI-style `tools` / `tool_calls`)
- Tools implemented: `bash` (bubblewrap/no-net, output limits + redaction), `read`, `write`, `web-search`, `web-fetch`, `fetch.summarize`, `tool.search`, `tool.enable`, `confirm.request`, `subagent.spawn`, `subagent.status`, `proactive.run`
- Proactive engine: built-in rules (daily brief/open loops/inactivity) + configurable custom agents (`agents:` in proactive rules YAML) with schedule_times/events/actions triggers
- Secrets vault (encrypted-at-rest) user commands: `/secret add|list|delete|clear`
- Redaction monitor for obvious token/password patterns (stores redacted content + warns)
- Signal link command: `/signal link|status|unlink` (gateway uses signal-cli daemon --http)
- Discord DM gateway + link commands: `/discord status|link|unlink`
- Memory commands: `/memory list|add|update|delete`
- Task shortcuts: `/task list|add|edit|done`
- Slash commands: `/help`, `/tools …`, `/subagent …`, `/secret …`, `/signal …`, `/discord …`, `/memory …`, `/task …`, `/admin …`, `/confirm …`

Next:
- Hardening/polish: more sandbox restrictions + tests
- Signal reliability + richer admin tooling
- Memory UX and retrieval improvements
- Richer proactive rules and escalation flows
