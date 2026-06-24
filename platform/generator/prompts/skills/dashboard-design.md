# Dashboard visual design

When generating a `kind: service` workflow that serves a dashboard, treat the
HTML/CSS the same way a hired design lead would — every dashboard should have a
specific point of view derived from what it's showing. Do not ship the
templated default.

## Ground the design in the subject

Before writing any markup, name the subject in one line: what data is on the
page, who reads it, what single decision or glance the page supports. Derive
palette, type, and structure from that subject — a news ticker, a system health
board, and a habit tracker should not look alike.

## Tokens (decide before writing CSS)

- **Palette**: 4–6 named hex values. One dominant surface, one ink, one
  accent that carries the personality, and 1–2 supporting tones. Avoid the
  three AI defaults: warm cream + serif + terracotta; near-black + acid
  green/vermilion; broadsheet hairlines with zero border-radius.
- **Type**: pair a *display* face with a *body* face deliberately, plus a
  *utility/mono* face if numbers or timestamps are on the page. Pull from
  Google Fonts (`<link rel="preconnect">` + `<link href="fonts.googleapis.com">`)
  or a system stack — name the families explicitly in the CSS, never leave
  `font-family` as the browser default.
- **Scale & spacing**: define a clear type scale (4–6 sizes) and a spacing
  rhythm. Hairline borders are fine but only if they encode structure.
- **One signature element**: pick the single moment the dashboard will be
  remembered by — a typographic treatment for the most important number, a
  custom indicator, an opinionated empty-state. Spend the boldness there and
  keep everything else quiet.

## Structure carries meaning

- The hero is a thesis. Open with the most characteristic thing on the page
  (the freshest number, the most urgent row, a live status), not a generic
  "Dashboard" title.
- Numbered markers (01 / 02 / 03), eyebrows, dividers — only if the content
  *is* a sequence or really has those sections. Otherwise drop them.
- Active voice in labels. Buttons name what they do ("Refresh now", not
  "Submit"). Empty states say what to do next, not "No data."

## Built-in quality floor (do these by default)

- Self-contained HTML in one response: inline `<style>`, no external JS bundle.
  External fonts and images are fine.
- `color-scheme: light dark;` or a deliberate single scheme — don't ship a
  half-styled light page on a dark OS.
- Responsive to ~360px wide. Single column collapses cleanly.
- `prefers-reduced-motion: reduce` respected — wrap transitions/animations in
  a media query.
- Visible `:focus-visible` outline on anything clickable.
- Auto-refresh: `<meta http-equiv="refresh" content="N">` or a small `setTimeout`
  reload — pick the cadence the data actually changes at, not 5s for hourly data.

## Anti-patterns to avoid

- Identical "card grid on dark background with one blue accent" for every
  dashboard regardless of subject.
- Decorative gradients/blur that don't carry information.
- "Lorem ipsum" or filler copy — write the real labels.
- Emojis as primary iconography (unless the subject genuinely calls for it).
- Re-skinning the platform's own UI (the platform shell is zinc + amber;
  dashboards are guests on their own page and should have their own voice).

## When the user pins direction

If the chat message specifies a look ("make it feel like a Bloomberg terminal",
"newspaper style", "Notion-like"), follow that exactly. The brief always wins.
