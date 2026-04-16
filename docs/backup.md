# Backup & restore

Tether stores most state in a single SQLite database file, but **secrets require the master key** and some integrations may have additional state.

## Create a backup

SQLite-only (consistent snapshot):

```bash
go run ./cmd/tether-backup --db ./data/tether.sqlite --out ./backups
```

Optional: include SSH portal config and per-user config/skills (sensitive):

```bash
go run ./cmd/tether-backup \
  --db ./data/tether.sqlite \
  --out ./backups \
  --include-config \
  --include-user-config

# WARNING: this additionally copies config material.
# Add --include-config-yaml if you also want config/tether.yaml copied.
```

The command writes:
- `tether-<timestamp>.sqlite`
- `tether-<timestamp>.manifest.json`

## What you must back up separately

- `TETHER_MASTER_KEY` (required to decrypt `/secret ...` vault entries)
- `OPENROUTER_API_KEY` (if not stored in config)
- `TETHER_SIGNAL_NUMBER` (if not stored in config)
- `TETHER_DISCORD_BOT_TOKEN` (if using Discord)
- If using Signal via `signal-cli`: the **signal-cli data directory** (commonly `~/.local/share/signal-cli`) so the account stays registered

## Restore

1. Stop Tether.
2. Replace `./data/tether.sqlite` with the backed up `.sqlite` file.
3. Ensure `TETHER_MASTER_KEY` is set to the *same* value as when the backup was made.
4. If using Signal, restore the `signal-cli` data directory.
5. Start Tether again.
