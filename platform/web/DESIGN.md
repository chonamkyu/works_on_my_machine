---
version: alpha
name: works-on-my-machine-design
description: >
  Workshop bench for engineer-authored AI workflows. Linear-base palette
  (near-black canvas #010102, hairline tiers, single lavender accent #5e6ad2)
  paired with Inter Tight display, JetBrains Mono for IDs / kbd / labels,
  and Noto Sans KR for Korean. The system reads as "engineer's tool, not
  consumer product": dense, keyboard-first, quietly luxurious. Chat is the
  hero; a right-side workshop panel (UI iframe + code tree + state + logs +
  ports map) docks beside it. Every surface state lives in the URL.
---

# works_on_my_machine — design system

This is the authoritative reference for the platform's UI tokens, layout
rules, and interaction patterns. If you're generating a dashboard inside a
workflow, see [`platform/generator/prompts/skills/dashboard-design.md`](../generator/prompts/skills/dashboard-design.md)
— that's the same spirit but applied to user-shipped UIs. This file
documents the platform shell itself.

## 1. Mood

The platform is the bench you stand at to commission and tend small AI
agents. The chat is the conversation with the agent; the right panel is the
bench top — iframe of the live UI, source tree, runs, logs, ports map. The
sidebar is the row of jars where each agent (task) waits, glyphed by status.

Tone:

- **Quiet chrome.** Hairlines instead of solid borders. One accent color.
  Never decorative gradients.
- **Keyboard first.** Every meaningful action has a `⌘`-something. The
  command palette is the second-order nav; the top bar is a sign, not a
  toolbar.
- **Information density without noise.** A monospace ID next to every
  task name. Status as a glyph + a color. No "Save" button in dialogs we
  could auto-commit.

## 2. Color

```
canvas              #010102   ← global background, deepest tone
surface-1           #0f1011   ← sidebar, top-bar, pane background
surface-2           #141516   ← cards, tab strip
surface-3           #18191a   ← selected sidebar row, hovered chip
surface-4           #1c1d1e   ← reserved for elevated overlays

hairline            #23252a   ← default 1px border
hairline-strong     #34343a   ← hover state for input borders
hairline-tertiary   #3e3e44   ← rarely used; subtle inner rules

ink                 #f7f8f8   ← primary text, active labels
ink-muted           #d0d6e0   ← body copy
ink-subtle          #8a8f98   ← meta, secondary labels
ink-tertiary        #62666d   ← captions, hints, faded mono

accent              #5e6ad2   ← Linear lavender. Active task border,
                                 focus ring, primary CTA bg, status badges
accent-hover        #828fff
accent-pressed     #5e69d1
accent-soft         #5e6ad2cc  ← softened for highlight backgrounds

status-running      #27a644   ← desaturated green (the "go" pulse)
status-waiting      #b89968   ← warm brass; cron / scheduled
status-idle         #62666d   ← same as ink-tertiary
status-failed       #d36b5f   ← rust red; never pure red
status-starting     #b89968
status-stopped      #3e3e44
```

**Hierarchy rule.** Surfaces step from `canvas → surface-1 → surface-2 →
surface-3` by tonal depth, not by hue. Never tint a surface with the accent
unless it's *the* CTA. Hairlines do the structural work.

**Status colors are state, not brand.** They appear only in glyphs (●◐○✕)
and stat badges. Don't borrow them for chrome.

## 3. Type

Three families, each with one job.

| family            | role               | weight  | use                                                 |
|-------------------|--------------------|---------|-----------------------------------------------------|
| Inter Tight       | sans display       | 500–700 | wordmark, task names, h1, button labels, body text  |
| JetBrains Mono    | mono               | 400–500 | task ids, timestamps, log lines, kbd hints, eyebrows |
| Noto Sans KR      | sans (Korean)      | 400–600 | falls in via font-family stack when text is Korean  |

Loaded once at app root from Google Fonts (`Inter+Tight`, `JetBrains+Mono`,
`Noto+Sans+KR`). No custom font hosting.

Scale (in px):

```
display-lg   28   500   tracking-tight   pane titles in service tabs
display      15   500   tracking-tight   chat header task name
title        14   500                    settings section heads
body         13   400                    chat bubbles, tab content
small        12   400                    table cells, secondary labels
caption      11   400                    sidebar meta, kbd hints
eyebrow      10   500   uppercase 0.18em mono   group labels (ACTIVE 03)
glyph        12   leading-none           status dots (●◐○✕)
```

Conventions:

- **Eyebrows are always mono uppercase with 0.18em tracking**. They label
  sections (`ACTIVE`, `TASK`, `RECENT RUNS`) — pin them to a column.
- **Numbers in mono.** Counts, durations, ports, ids.
- **Korean body matches surrounding density**: never larger than its Latin
  neighbor in the same row.

## 4. Spacing & radius

```
2  4  8  12  16  20  24  32  40  48   (px tokens; 4-multiple)
```

Rounded corners are minimal:

```
sm        4px    nested chips
md        6px    buttons, inputs
lg        8px    cards, panels
xl       12px    palette / overlay panel
pill   9999px    glyph wrappers, kbd
```

Sharp corners on top-bar and tab strips — they belong to the structural
frame, not floating cards.

## 5. Layout

```
┌─ TopBar 44px ─────────────────────────────────────────────────────────┐
│ works_on_my_machine        [⌘K Search or jump…]   [⚙]   @nk          │
├──────────────┬─────────────────────────────────────┬──────────────────┤
│              │                                     │                  │
│  Sidebar     │   ChatPane                          │   UIPane         │
│  244px       │   flex                              │   flex           │
│  (collapses  │                                     │   (collapses     │
│   to 40px    │   ┌─ task name · session id        │    when ⌘.)      │
│   icon rail) │   │ msg stream                      │                  │
│              │   │ streaming preview while pending │   tabs ────────  │
│  ── active   │   │ prompt input                    │   ui · code ·    │
│  ◐ task-a    │   └                                 │   state · ports  │
│  ● task-b    │                                     │   triggers · …   │
│              │                                     │                  │
│  ── idle     │                                     │   panel body     │
│  ○ task-c    │                                     │                  │
└──────────────┴─────────────────────────────────────┴──────────────────┘
```

- **Sidebar** (`244px` open, `40px` collapsed) groups tasks by activity
  bucket: `active`, `idle`, `stopped`. Each group has a mono eyebrow with a
  zero-padded count (`active 03`). Selected row uses `surface-3` bg, no
  border.
- **ChatPane** is `flex-1`. The task name is editable inline (click to
  edit, Enter saves). Session id sits next to it in mono `ink-tertiary`.
- **UIPane** is `flex-1`, docked right. Tabs are top-aligned. The pane has
  exactly one chevron (`<<`) to collapse; expansion is via a chevron in
  the chat header, `⌘.`, or `⌘U`. Default is **open**.
- **Overlay surfaces** (`CommandPalette`, `SettingsOverlay`) center over a
  `canvas/70 backdrop-blur-sm` scrim and trap focus.

Mobile (< 768px): sidebar and pane become full-screen drawers. The chat
remains the always-visible default.

## 6. Components

### Buttons

```
PRIMARY        bg-accent text-white         hover:bg-accent-hover     h-7  px-3   rounded-md
GHOST          text-ink-muted               hover:bg-surface-2        h-7  px-2.5 rounded-md
DESTRUCTIVE    text-ink-tertiary            hover:text-status-failed
                                            hover:bg-status-failed/10 h-7  px-3   rounded-md  (Archive)
ICON           text-ink-subtle              hover:text-ink            w-7  h-7    rounded-md   (chevrons, cog)
```

- A button gets `font-medium` only when it's primary. Ghosts stay 400.
- Keyboard hint sits in a sibling `<kbd>` styled as
  `font-mono text-[9-10px] text-ink-tertiary`. Never combined into the
  button label.
- Disabled = `opacity-50`, no pointer changes.

### Inputs

```
bg-surface-1 border border-hairline rounded-md
text-ink placeholder:text-ink-tertiary
focus:border-accent  outline-none
h-7 px-2.5 text-[12px] (default mono for paths/ids)
```

The global `:focus-visible` rule paints a 2px accent ring. Big inputs (the
command palette searchbox, chat textarea) opt out explicitly with
`outline-none`.

### Glyphs (task status)

```
●  status-running   green pulsing dot when live
◐  status-waiting   half-fill — scheduled / queued
○  status-idle      hollow — never run yet
✕  status-failed    red ex — terminal failure
◌  status-starting  bracket — booting
■  status-stopped   solid square — disabled
```

Always 12px line-height-none, color from the matching `status-*` token.
Both glyph and color are used (a11y — color blindness).

### Tabs (UIPane)

```
inactive   text-ink-subtle  hover:text-ink-muted
active     bg-surface-2     text-ink
h-7 px-3 rounded-md font-mono text-[12px]
```

Tab strip is horizontally scrollable on narrow widths. Single-row only.

### Code blocks (in chat)

```
container  bg-surface-950/60 border border-hairline rounded-md
header     bg-surface-900/60 border-b border-hairline
           [language label · mono · 10px uppercase ink-tertiary] [copy]
body       font-mono text-[12px] text-ink leading-relaxed  overflow-x-auto
```

Inline code: `font-mono text-[12px] px-1 py-0.5 rounded bg-surface-3
border border-hairline text-accent-hover`. Used sparingly — agent replies
should reach for fences, not inline.

### Cards (sidebar rows, run rows, message bubbles)

No card shadows. Cards are flat surfaces separated by:

- Background tone (`surface-2` for hovered, `surface-3` for active)
- Hairline above for grouped lists
- Mono eyebrow label above the group

If you reach for a `shadow-*` utility, you're probably wrong. Shadows are
only allowed on full-screen overlays (palette, settings modal).

## 7. Motion

- **Pulse** on the `running` status glyph (`animate-pulse`) and on tasks
  that just fired a trigger (1500ms decay).
- **No page-load orchestration.** The chat needs to be reachable; staggered
  reveals would feel slow.
- **Reduced motion is respected** at the CSS root — all transitions clamp
  to 0.01ms when the user prefers reduced motion.

Hover transitions are limited to `colors` and the implicit chrome change
(border + bg). Never animate position or size for ambient effects.

## 8. Keyboard

| binding | action                                                              |
|---------|---------------------------------------------------------------------|
| `⌘K`    | command palette (tasks + memory + evolution + discover + settings)  |
| `⌘,`    | settings overlay                                                    |
| `⌘.`    | toggle right panel                                                  |
| `⌘U`    | toggle right panel (alias for `⌘.`)                                 |
| `⌘B`    | toggle sidebar (full ↔ icon rail)                                   |
| `⌘N`    | new task                                                            |
| `⌘↵`    | send chat message                                                   |
| `↑↓`    | move cursor in palette / file tree                                  |
| `Esc`   | dismiss palette / overlay / inline rename                           |

⌘1 / ⌘2 / ⌘3 are intentionally **not** bound — they collide with browser
tab navigation and were a known source of accidental overlays.

## 9. URL state

Every layout choice is a search param so refresh and link-sharing restore
the workshop exactly:

| param      | values                            | meaning                                  |
|------------|-----------------------------------|------------------------------------------|
| `task`     | `tsk_<id>`                        | selected task                            |
| `pane`     | `0` (collapsed) / absent (open)   | right panel visibility                   |
| `paneTab`  | `ui` `code` `state` `ports` `triggers` `runs` `logs` `caps` | active panel tab |
| `overlay`  | `settings` `memory` `evolution` `discover` | settings modal tab               |
| `sidebar`  | `0` (collapsed) / absent (open)   | left rail visibility                     |

`paneTab` and `overlay` default to a sensible auto pick (UI for service
tasks, state for oneshot; settings for the cog button). They appear in the
URL only after the user changes them.

## 10. Voice

- **Korean is the user's primary language.** When the user writes in
  Korean, the agent replies in Korean by default. Buttons / labels stay in
  English. Per-user skill overlay can pin Korean reply tone for everything.
- **Terse, never cheery.** No exclamation marks in chrome. Errors don't
  apologize. Empty states tell the user what to do (`pick a task on the
  left, or start a new one`).
- **Mono for identifiers, sans for content.** Don't mix.

## 11. References

- Linear (linear.app) — palette, hairline structure, panel docking
- Raycast — command palette ergonomics
- Anthropic Claude — chat bubble rhythm, agent attribution patterns

For workflow-author dashboard guidance see the catalog at
<https://github.com/voltagent/awesome-design-md> — the agent can pull
specific brand tokens when the user pins a look ("Stripe-style table",
"newspaper feel", etc.) but the platform itself stays Linear-base.
