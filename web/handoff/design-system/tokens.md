# Aviary Slate — Design Tokens

All tokens are CSS custom properties defined in `src/app/globals.css`.
They switch between light and dark via `:root[data-theme="dark|light"]`.
An optional `:root[data-accent="green"]` override swaps the accent hue.

**Usage rule**: never inline hex values in components. Always reference
`var(--token-name)` or the Tailwind alias that wraps it.

---

## Canvas & surfaces (dark)

| Token | Value | Usage |
|---|---|---|
| `--bg-canvas` | `#0F1115` | Outermost app bg, header, main scroll area |
| `--bg-surface` | `#14161C` | Sidebar, side panels, persistent chrome |
| `--bg-raised` | `#191C23` | Cards, popovers, dialogs |
| `--bg-sunk` | `#0B0D11` | Inputs, code blocks, chat composer |
| `--bg-hover` | `rgba(255,255,255,0.04)` | Hoverable row / button overlay |
| `--bg-active` | `rgba(255,255,255,0.07)` | Active / pressed / selected |
| `--bg-overlay` | `rgba(8,10,14,0.72)` | Dimmer behind modals & palette |

## Canvas & surfaces (light)

| Token | Value | Usage |
|---|---|---|
| `--bg-canvas` | `#FAF9F7` | Warm off-white — not pure white |
| `--bg-surface` | `#F4F2EE` | Sidebar |
| `--bg-raised` | `#FFFFFF` | Cards |
| `--bg-sunk` | `#EDEAE4` | Inputs, code |
| `--bg-hover` | `rgba(20,22,28,0.04)` | — |
| `--bg-active` | `rgba(20,22,28,0.07)` | — |
| `--bg-overlay` | `rgba(60,55,45,0.20)` | Warm-toned dimmer |

## Borders (both themes)

Three levels — all `rgba(white, α)` in dark, `rgba(60,55,45, α)` in light.

| Token | Dark α | Light α | Usage |
|---|---|---|---|
| `--border-subtle` | 0.06 | 0.08 | Section dividers, card outline |
| `--border-default` | 0.09 | 0.12 | Inputs, buttons, most borders |
| `--border-strong` | 0.15 | 0.22 | Hover-state borders, scrollbar thumb hover |

## Foreground (text)

| Token | Dark | Light | Usage |
|---|---|---|---|
| `--fg-primary` | `#ECEDF0` | `#1B1D22` | Body text, headings |
| `--fg-secondary` | `#B4B7C0` | `#4E525C` | Secondary labels, sidebar inactive |
| `--fg-tertiary` | `#878A94` | `#6F7380` | Metadata, timestamps |
| `--fg-muted` | `#5E616C` | `#9A9EA8` | Disabled, placeholder, overline |
| `--fg-inverse` | `#0F1115` | `#FAF9F7` | Text on filled primary button |

## Accent (primary interactive)

| Token | Dark | Light | Usage |
|---|---|---|---|
| `--accent-blue` | `#5B8DEF` | `#3B6FD8` | Primary buttons, links, focus |
| `--accent-blue-strong` | `#7BA5F5` | `#2E5BBF` | Hover state on primary |
| `--accent-blue-soft` | `rgba(…, 0.14)` | `rgba(…, 0.10)` | Focus ring, ghost active |
| `--accent-blue-border` | `rgba(…, 0.35)` | `rgba(…, 0.30)` | Focused input border |

Optional `data-accent="green"` override swaps to `#2FA46A` / `#3FB57B`.

## Status

| Token | Dark | Light | Semantic |
|---|---|---|---|
| `--status-live` | `#4ADE80` | `#2FA46A` | Running, online, success |
| `--status-warn` | `#F5B454` | `#C9801E` | Warning, pending |
| `--status-error` | `#F07A7A` | `#D25151` | Failed, error |
| `--status-info` | `#5B8DEF` | `#3B6FD8` | Informational (same as accent) |

Each has a matching `-soft` variant at ~12-15% alpha for pills, backgrounds,
and dot halos.

## Ownership badges

| Token | Value (dark) | Used by |
|---|---|---|
| `--badge-private-bg` / `-fg` | neutral at 10% / `#B4B7C0` | Private assets |
| `--badge-published-bg` / `-fg` | blue at 15% / `#95B4F5` | Published assets |
| `--badge-imported-bg` / `-fg` | green at 13% / `#7DDC9E` | Imported assets |

## Shadows

All shadows are paired with a hairline border via a second box-shadow layer.

| Token | Value (dark) |
|---|---|
| `--shadow-sm` | `0 1px 2px rgba(0,0,0,0.35)` |
| `--shadow-md` | `0 4px 12px rgba(0,0,0,0.40), 0 0 0 1px rgba(255,255,255,0.04)` |
| `--shadow-lg` | `0 12px 32px rgba(0,0,0,0.55), 0 0 0 1px rgba(255,255,255,0.06)` |
| `--shadow-xl` | `0 24px 56px rgba(0,0,0,0.65), 0 0 0 1px rgba(255,255,255,0.08)` |

Light theme shadows are warm-toned (`rgba(60,55,45, …)`) and much softer.

---

## Non-token constants

These are part of the system but not tokenized (they never change by theme):

**Radius**: 4 · 5 · 6 · 7 · 10 · 12 · 99 (pill)
- 7px — buttons, inputs
- 10px — cards
- 12px — dialogs, palette

**Spacing**: 4 · 8 · 12 · 16 · 20 · 24 · 32 · 40 · 56 · 72
(i.e. Tailwind `1 2 3 4 5 6 8 10 14 18`)

**Transition**: `120ms` for hovers, `180ms cubic-bezier(0.16, 1, 0.3, 1)`
for panel slides, `220ms` for dialog enter/exit.

**Focus ring**: `0 0 0 3px var(--accent-blue-soft)` — never an outline.

---

## Tone palette (identity colors)

8 tones, used for agent / workflow avatars. Each is a tinted background +
saturated foreground. Never re-roll — assigned once per asset and stored on
the record.

| Tone | Dark bg α | Dark fg | Light bg α | Light fg |
|---|---|---|---|---|
| blue   | `rgba(91,141,239, 0.18)`  | `#7BA5F5` | `rgba(59,111,216, 0.13)`  | `#2E5BBF` |
| green  | `rgba(74,222,128, 0.18)`  | `#7DDC9E` | `rgba(47,164,106, 0.15)`  | `#1F7A4E` |
| amber  | `rgba(245,180,84, 0.18)`  | `#E8AD5F` | `rgba(201,128,30, 0.14)`  | `#8A5B13` |
| pink   | `rgba(236,122,178, 0.18)` | `#E89BC0` | `rgba(200,85,145, 0.12)`  | `#A1467A` |
| purple | `rgba(148,124,232, 0.18)` | `#AA96E8` | `rgba(110,88,196, 0.13)`  | `#5A448F` |
| teal   | `rgba(79,201,204, 0.18)`  | `#7BCFD1` | `rgba(40,140,145, 0.13)`  | `#206266` |
| rose   | `rgba(232,112,112, 0.18)` | `#E89595` | `rgba(188,72,72, 0.12)`   | `#8F3838` |
| slate  | `rgba(150,155,170, 0.16)` | `--fg-secondary` | `rgba(90,95,105, 0.10)` | `--fg-secondary` |

Rendered as a single `.tone-<name>` CSS class that sets both `background`
and `color`. Works on any inline-block element.

---

## Tailwind aliases

`tailwind.config.ts` exposes these tokens as first-class Tailwind colors
so you can write `bg-surface` instead of `bg-[var(--bg-surface)]`:

```ts
colors: {
  canvas: "var(--bg-canvas)",
  surface: "var(--bg-surface)",
  raised: "var(--bg-raised)",
  sunk: "var(--bg-sunk)",
  border: {
    subtle: "var(--border-subtle)",
    DEFAULT: "var(--border-default)",
    strong: "var(--border-strong)",
  },
  fg: {
    primary: "var(--fg-primary)",
    secondary: "var(--fg-secondary)",
    tertiary: "var(--fg-tertiary)",
    muted: "var(--fg-muted)",
  },
  accent: {
    DEFAULT: "var(--accent-blue)",
    strong: "var(--accent-blue-strong)",
    soft: "var(--accent-blue-soft)",
    border: "var(--accent-blue-border)",
  },
  status: {
    live: "var(--status-live)",
    warn: "var(--status-warn)",
    error: "var(--status-error)",
    info: "var(--status-info)",
  },
}
```

shadcn's `primary / secondary / destructive / muted / accent` aliases also
map onto these in `globals.css` under `@layer base`.
