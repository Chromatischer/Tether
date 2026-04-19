# Tether TUI — Frontend Design Spec
**Date:** 2026-04-19  
**Scope:** Full visual pass on `internal/tui/` — styles, chat, auth, richtext, app header  
**Status:** Approved

---

## Design System

### Theme: Ember
Charcoal black base with amber/orange accent. Terminal-native — every decoration element uses glyphs, borders, and color rather than graphics. Feels like a polished dev tool, not a web app.

### Color Palette (ANSI 256 + truecolor fallbacks)

| Role | Color | ANSI |
|---|---|---|
| Background | `#111` / near-black | `233` |
| Header bg | `#0e0e0e` | `232` |
| Bot message bg | `#161616` | `234` |
| User message bg | `#0f1a12` | dark green tint |
| Tool strip bg | `#131313` | `233` |
| Amber accent | `#e5890a` | `172` |
| Green (user / success) | `#3fb950` | `71` |
| Red (error) | `#f85149` | `203` |
| Border | `#1e1e1e` | `235` |
| Tool border | `#252525` | `236` |
| Muted text | `#555` | `240` |
| Dim text | `#333` | `236` |
| Body text | `#ccc` | `252` |
| User body text | `#a3e4b2` | `115` |

---

## Header Bar

**Layout:** Single row, full terminal width, 1 line tall (no change to existing `h-1` offset).

```
┌──────────────────────────────────────────────────────────────────┐
│ TETHER │ ▸ Chat │ Memory │ Settings │              ● username    │
└──────────────────────────────────────────────────────────────────┘
```

- **Brand block**: Amber fill (`#e5890a`), black text (`#0a0a0a`), bold, `TETHER`, letter-spacing 2. Flush to the left edge with horizontal padding.
- **Tabs**: Stretch to fill remaining width. Inactive: `#2e2e2e`. Active: white text, `#161616` background, 1px amber top-border, `▸` prefix glyph, bold.
- **Spacer**: Expands between tabs and user badge.
- **User badge**: Right-aligned. `●` dot in `#3fb950` (green = online), username in `#333`.
- **Pre-auth**: Same layout, tabs replaced with `Login` / `Sign up` (no user badge).

### Implementation notes
- `renderHeader()` in `app.go` builds the bar with lipgloss joins.
- `styleHeaderBrand` gets a `Background(amber).Foreground(black)` treatment.
- Active tab style gains `BorderTop(true)` + amber border color + slightly lighter background.
- User badge extracted as a right-aligned styled string appended after the spacer.

---

## Chat Screen

### Banner
Single dim line above the transcript. Dark background (`#0d0d0d`), very dim text. Shows keyboard shortcut hints compactly:

```
  tab  autocomplete    /  commands    $  skills    ^O  reasoning
```

Keys rendered with a subtle `#3a3a3a` background box (keyboard glyph style).

### Message Strips — Full-width, left-border coded

Every message bleeds edge-to-edge. The left border is the sole color signal for sender identity.

**Bot / assistant** (`role == "assistant"`):
- Background: `#161616`  
- Left border: 2px solid amber `#e5890a`
- Sender label: `◆ tether` — amber, 9px, bold, above body text
- Body: `#ccc`

**User** (`role == "user"`):
- Background: `#0f1a12`
- Left border: 2px solid green `#3fb950`  
- Sender label: `you ›` — green, 9px, bold
- Body: `#a3e4b2`

**System** (`role == "system"`):
- Background: `#111`
- Left border: 2px solid `#2a2a2a`
- Sender label: `● system` — `#333`, 9px
- Body: `#3a3a3a`, italic

**Error** (system messages that are errors):
- Detection: content begins with `"(agent error)"`, `"failed"`, or `"✗"` prefix — no new field needed
- Left border: red `#f85149`
- Sender label: `✗ error` — red
- Body: dark red tint

**Info** (system messages that are confirmations/success):
- Detection: content begins with a known success prefix (`"Signal linked"`, `"Discord linked"`, `"task marked done"`, etc.) — simple `strings.HasPrefix` list in `formatMessage`
- Left border: green `#3fb950`
- Sender label: `✓ info` — green

### Tool Call Strips — Quiet Ghost

Tool invocations use a two-line pattern that whispers in the background without interrupting the conversation flow.

**Invocation line:**
- Background: `#131313`
- Left border: 2px solid `#252525` (barely visible)
- Content: `▷  <tool-name>  ·  <args>` — tool name `#484848`, args `#333`, icon amber-dim
- Font size: slightly smaller (10.5px)

**Result line** (indented, appears below):
- Background: `#111`
- Left border: 2px solid `#1e1e1e`
- Left padding: 26px (indented past the icon)
- Content: `✓ <summary>` (green dim) or `✗ <error>` (red dim), italic, 9.5px

**Streaming (in-progress):**
- Result line shows `·  running…` in `#2a2a2a` italic until result arrives
- On completion, result line updates in-place

### Reasoning Block
Collapsed by default. When expanded (Ctrl+O or click):
- Background: `#0e0e0e`
- Left border: 2px amber
- Header: `◈ model reasoning` (dim) + `^O to collapse` hint (very dim), flex row
- Body: reasoning text in `#3a3a3a`, italic, slightly smaller

Collapsed state shows: `◈ model reasoning available  ^O to expand` as a single dim line (same styling).

### Streaming Indicator
While the assistant is generating with no text yet, show an animated dot pulse instead of `"..."`:

```
◆ tether
● ● ●   ← three amber dots, staggered opacity animation
```

Each `●` fades in/out with 200ms offset. **Implementation:** Add a `streamFrame int` field to `chatModel`. A `streamTickCmd()` fires every 120ms (via `tea.Tick`) whenever any streaming assistant message exists, incrementing the frame counter and triggering a `reflow()`. The three dots select from a 4-level amber opacity palette based on `(frame + offset) % 8`. The ticker stops when no streaming messages remain.

### Autocomplete Dropdown
Appears above the composer when suggestions are active.

- Background: `#0e0e0e`
- Border: 1px solid `#1e1e1e`, rounded top corners, flat bottom (flush to composer)
- Inactive item: label `#444`, detail `#2a2a2a`, padding `3px 10px`
- Active item: label `#e5890a` bold, detail `#555`, background `#181818`, left border 2px amber
- Max ~6 items visible; scrolls if more

### Composer
```
┌─────────────────────────────────────────────────────┐  ← top border #1a1a1a
│ ❯  Type a message…▌                      [enter]    │
│  tab  cycle    ↵  apply    esc  dismiss   ^O  reason │
└─────────────────────────────────────────────────────┘
```

- Background: `#0e0e0e`
- `❯` prompt symbol: amber `#e5890a`, slightly larger
- Input: `#888` placeholder, `#ccc` active text
- `[enter]` key badge: amber text, `#2a2a2a` border, `2px` rounded
- Cursor: amber block cursor, blinking
- Hints row: dim keyboard glyph badges (`#181818` bg, `#222` border) + `#222` description text

---

## Auth Screen

The auth screen centers content vertically with the ASCII logo above the login box.

### Logo
The existing `tetherLogo` figlet art, rendered in amber `#e5890a`. Below it: tagline `personal AI over SSH` in very dim `#2a2a2a`.

### Login Box
```
┌──────────────────────────┐
│ ▸ Login     Sign up      │  ← tab-style mode switcher
├──────────────────────────┤
│ username   chromatischer │  ← field rows
│ password   ••••••••      │
├──────────────────────────┤
│          LOGIN  →        │  ← amber submit button
└──────────────────────────┘
```

- Box border: `#1e1e1e`, rounded corners
- Header strip: `#0e0e0e` bg, active mode in amber + `▸` glyph, inactive mode in `#2a2a2a`
- Field rows: alternating very subtle bg, label in `#333` fixed 52px width, value in `#888` / `#ccc` when focused
- Focused field: amber underline on value area
- Submit button: full-width amber bg, black bold text, `→` suffix
- Below box: `no account? sign up` in dim text / slightly lighter link

---

## Rich Text Rendering

No structural changes — the existing Markdown renderer in `richtext.go` is kept. Style updates only:

- **Code inline**: amber-tinted text `#d4a85a` on `#161616` bg (instead of current panel color)
- **Code block**: same bg, amber-dim border-left instead of plain border
- **Headings h1**: white `#fff`; h2: `#ccc`; h3+: `#888`
- **Bold**: bright white `#fff` (assistant) / green `#a3e4b2` (system)
- **Links**: amber underlined
- **HR rule**: dim amber `─` repeat
- **Table borders**: `#252525`

---

## Status Bar (stretch goal — bottom of chat)

A single line inside the composer area, below the hints row, showing live agent state. **Only rendered when non-empty — contributes zero height when idle, so it never shifts the composer layout.**

```
  ● 1 active run     ◈ reasoning     last: 14:32
```

- Condition: `appModel.activeRuns > 0` OR pending confirmation
- Colors: green `●` for active run count, amber `◈` for reasoning active, dim `#333` for last-reply timestamp
- Implementation: `renderStatus()` helper on `appModel` returns `""` when idle; when non-empty the composer height calculation in `withSize()` must account for the extra line

---

## Symbols Reference

| Glyph | Usage |
|---|---|
| `◆` | Tether bot sender |
| `›` | User sender separator |
| `▸` | Active tab prefix, active mode prefix |
| `▷` | Tool call invocation |
| `◈` | Reasoning toggle |
| `●` | Online indicator, streaming dots |
| `✓` | Success result |
| `✗` | Error result |
| `❯` | Composer prompt |
| `[enter]` | Send key badge |

All glyphs are in standard Unicode and render correctly in any terminal with a modern font. No Nerd Font dependency.

---

## Files Changed

| File | Change |
|---|---|
| `internal/tui/styles.go` | Full palette replacement, all style variables updated |
| `internal/tui/app.go` | `renderHeader()` — block brand, active tab top-border, user badge |
| `internal/tui/chat.go` | `formatMessage()` — strip layout, sender glyphs, tool ghost, streaming dots, autocomplete styling |
| `internal/tui/auth.go` | `View()` — amber logo, box tab switcher, field rows, amber button |
| `internal/tui/richtext.go` | Style function updates for new palette |

No new files. No logic changes — purely visual layer.
