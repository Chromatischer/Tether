# AGENTS.md

Guidance for AI agents working in this repo.

## DB migrations: release baseline policy

The app embeds SQLite migrations from `internal/db/migrations/*.sql` and runs
them at server startup. Applied versions are recorded in `schema_migrations`.

This repo does **not** keep an unbounded migration history forever. Each
supported release line has a compact active migration set:

1. one baseline schema snapshot for that release line
2. any forward migrations after that baseline

The current baseline is declared in `internal/db/migrate.go`:

```go
var currentSchemaBaseline = schemaBaseline{
	Release: "v0.6",
	Version: 17,
}
```

The baseline SQL file must use the same numeric version and release id, e.g.
`internal/db/migrations/0017_v0_6_init.sql`.

Important rules:

- Migration numbers never reset. Existing databases may already have older
  version numbers recorded, so future files must keep increasing.
- Always pick the next migration number by looking at the highest existing file
  in `internal/db/migrations` and adding one.
- Fresh databases apply the baseline snapshot first, then later migrations.
- Existing databases at or above the baseline skip the baseline and apply only
  later migrations.
- Existing databases below the active baseline are intentionally rejected by
  the migration runner. They must first upgrade through a compatible earlier
  release that can bring them to the baseline.
- Do not edit a shipped migration in place. Add a new forward migration unless
  the file truly has not shipped anywhere.

When creating a new baseline for any release id:

1. Start from a fully migrated database for the previous release line.
2. Dump or hand-write the full current schema as one snapshot migration.
3. Name it with the latest migration number and release id, such as
   `0024_v0_7_init.sql`.
4. Delete older migration files from the active set.
5. Update `currentSchemaBaseline.Release` and `currentSchemaBaseline.Version`
   to match the new snapshot file.
6. Add future migrations above that number, e.g. `0025_*.sql`.

Keep this section in sync with any migration policy change.

## TUI: lipgloss background rules

The TUI uses `charm.land/lipgloss/v2`. Background colors in lipgloss are easy
to apply *partially* and look correct in isolation, then visibly fail when the
piece is composed into a larger layout. The same trap is also wasted hours of
debugging on this codebase. Internalize these rules before touching any
view that has a colored background.

### The underlying mechanic

`Style.Render` emits the background ANSI code **once at the start of each
line** and a `[reset]` at the end. Every nested child's `[reset]` clears the
background for the rest of the line. So background only "sticks" on cells
that are *inside* a span that explicitly sets it — never in the gaps between
spans, never in trailing padding added by an outer layer.

### Layout primitives don't share a whitespace-styling story

| Primitive                       | Whitespace handling                              |
| ------------------------------- | ------------------------------------------------ |
| `Style.Render` (with `Width`)   | Pads with the style's background. ✓             |
| `Place` / `PlaceHorizontal`     | Accepts `WithWhitespaceStyle(...)`. Use it. ✓   |
| `JoinVertical` / `JoinHorizontal` | Pads with **plain unstyled spaces**. ✗         |

`JoinVertical(Center)` and `JoinHorizontal(Center)` are the silent killers:
when siblings have unequal widths, they inject plain-space padding that you
cannot style after the fact.

### The rule

Every element that participates in a colored layout must satisfy **both**:

1. **Explicit background on the style itself.** A foreground-only style (e.g.
   `lipgloss.NewStyle().Foreground(c)`) leaves the rendered characters'
   background as terminal default. This shows up as a ~1-shade-off rectangle
   over your text.

2. **Pre-padded to a uniform width before joining.** Compute
   `blockWidth = max(width of every sibling)` and wrap each sibling with
   `lipgloss.PlaceHorizontal(blockWidth, pos, child, lipgloss.WithWhitespaceStyle(bg))`
   *before* passing them to `JoinVertical`. Then JoinVertical never has to
   insert a single plain space.

   Don't forget *every* sibling — including the one-line hint at the bottom
   that's secretly 1 column wider than the logo.

3. **Use `WithWhitespaceStyle` on outer `Place` calls** to fill the screen
   bg around the centered block. Don't wrap the result in
   `bgStyle.Render(placed)` as a substitute — that suffers from rule (1) at
   the line level (resets from inner content clear the bg before the trailing
   spaces).

### Debugging checklist

When you see "background is missing somewhere":

- **Around the whole block?** Outer `Place` is missing `WithWhitespaceStyle`
  (or the wrapper `bgStyle.Render(...)` is being used instead).
- **On the characters of an element?** The element's style is fg-only — add
  `Background(...)`.
- **One-column strip on the right edge?** A sibling is wider than what you
  passed as `blockWidth`; `JoinVertical` is padding the rest with one plain
  space. Recompute the max across **all** siblings.
- **Holes inside a styled row, between spans?** Two adjacent inner spans
  have different (or missing) backgrounds, and the outer wrapper's bg got
  cleared by an inner `[reset]`. Give every inner span its own complete
  bg, or build the row from a single Style.Render rather than concatenating.

### Anti-pattern: trailing `bgStyle.Render(placed)`

```go
// Looks reasonable. Does not work.
bgFill := lipgloss.NewStyle().Background(colorBg)
return bgFill.Render(lipgloss.Place(w, h, Center, Center, block))
```

The `Render` wraps each line with `[bg=colorBg]…[reset]`. Any inner reset
inside the line strips the bg before the line ends, so trailing padding
shows terminal default. Use `lipgloss.WithWhitespaceStyle` on `Place`
itself instead.
