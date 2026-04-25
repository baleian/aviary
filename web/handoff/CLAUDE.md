# Aviary Web — Project Instructions for Claude Code

You are working on the **Aviary** web app. This file is loaded automatically
for every Claude Code session in this repo.

## What this app is

Aviary is an internal platform for building, running, and sharing AI **agents**
and **workflows**. Four primary surfaces:

- **Dashboard** — at-a-glance: chat sessions, workflow runs, published agents
  & workflows (with install counts), activity
- **Agents** — list → detail (chat + workspace) → editor
- **Workflows** — list → detail (runs + stats) → builder (node graph)
- **Marketplace** — discover, filter, install agents & workflows published by
  other teams

Users have three ownership states for any asset: **private** (mine, unpublished),
**published** (mine, live on marketplace), **imported** (someone else's,
installed into my workspace).

## Stack (do not change without asking)

- Next.js 15 App Router + React 19
- Tailwind 3 + `tailwindcss-animate` + `@tailwindcss/typography`
- **shadcn/ui** (style: `new-york`, base color: `neutral`, CSS variables ON)
- `@xyflow/react` for the workflow graph builder
- `@monaco-editor/react` for code editors
- `react-diff-viewer-continued` for diffs
- `@dnd-kit/*` for node graph drag
- `lucide-react` icons
- Fonts: **Inter** (UI) + **JetBrains Mono** (code). See `src/app/fonts.ts`.

## Design System — "Aviary Slate"

This repo has a **complete design system package** at `/design-system/` in the
handoff bundle. Read it before touching any UI. The short version:

- **Theme**: dark slate (`#0F1115` canvas, `#14161C` surface, `#191C23` raised)
  with a warm off-white light theme (`#FAF9F7`). User-togglable, persisted
  via `data-theme` on `<html>`.
- **Accent**: single blue (`#5B8DEF` dark / `#3B6FD8` light). No gradients.
  Optional green accent via `data-accent="green"` but blue is default.
- **Typography**: Inter at 13.5px base / line-height 1.5. Type scale:
  `.t-hero` 24 · `.t-h1` 20 · `.t-h2` 16 · `.t-h3` 14 · `.t-body` 13.5 ·
  `.t-small` 12.5 · `.t-xs` 11.5 · `.t-over` 10.5 uppercase.
  Tabular numerals everywhere numeric.
- **Radius scale**: 4 / 5 / 6 / 7 / 10 / 12 / 99 (pill). Buttons & inputs
  use 7px; cards 10px; overlays 12px. Tight, not soft.
- **Spacing**: 4 / 8 / 12 / 16 / 20 / 24 / 32. No 10, 14, 18 etc.
- **Borders**: three levels — `subtle` / `default` / `strong` — all rgba,
  driven by tokens. Never hardcode border colors.
- **Shadows**: four levels, always paired with a hairline border via
  `box-shadow: … , 0 0 0 1px rgba(…)`.
- **Status colors**: green (live/running), amber (warn), red (error), blue (info).
- **Tone avatars**: 8 hues (blue, green, amber, pink, purple, teal, rose, slate)
  used for agent/workflow identity tiles. Never re-roll — the tone is part of
  the asset's identity and stored on the agent/workflow record.

## Design principles (enforced)

1. **No filler.** Every pixel earns its place. If a section feels empty, fix
   the layout, don't invent content.
2. **No gradients** on chrome or text. Tone avatars use solid tinted fills;
   buttons are flat.
3. **No emoji.** Use `lucide-react` icons. If the icon doesn't exist, draw
   an SVG that matches lucide's 1.5px stroke, 24px grid.
4. **Tabular numerals** on every number: `font-variant-numeric: tabular-nums`
   or `font-feature-settings: "tnum"`.
5. **Hover = subtle tone shift**, never a color swap. `bg-hover` / `bg-active`
   tokens exist for this.
6. **Active nav** gets a 2px left rail in accent, not a filled pill.
7. **No rounded corners on dense UI** (sidebar items 7px, header 0, panels 0,
   graph canvas 0).
8. **Density over breathing room.** This is a tool, not a landing page.
   Header 48px, row heights 32-40px, card padding 14-16px.

## Route model

Use a single app-wide **route object** — not URL-driven state. Two reasons:
(a) most views are modals/drawers over the same shell, (b) the design
depends on animated transitions between views that pure URL routing fights.

```ts
// src/features/layout/lib/route.ts
export type Route = {
  primary: "dashboard" | "agents" | "agent" | "workflows" | "workflow" |
           "marketplace" | "marketplace-item";
  agentId?: string;         // when primary="agent"
  agentTab?: "chat" | "editor" | "runs" | "settings";
  workflowId?: string;      // when primary="workflow"
  workflowTab?: "overview" | "builder" | "runs" | "settings";
  marketplaceId?: string;   // when primary="marketplace-item"
  marketplaceFilter?: "all" | "agents" | "workflows" | "mine";
  sessionId?: string;       // deep-link into an agent chat session
};
```

Top-level `<RouteProvider>` holds `{route, setRoute}` in React Context.
**Never** call `router.push` to change screens — call `setRoute(...)`.
The URL is synced as `?p=dashboard&agent=a1` by a small effect on the
provider; links share cleanly but the source of truth is the object.

## Naming conventions

- Components: **PascalCase** files, named export matching the file:
  `AgentCard.tsx` → `export function AgentCard()`.
- Hooks: `use-*.ts`, named export `useFoo`.
- Types: `src/types/<domain>.ts` — one file per domain
  (`agent.ts`, `workflow.ts`, `chat.ts`, ...).
- API clients: `src/features/<domain>/api/*.ts` — each a thin fetch wrapper
  over `src/lib/http/client.ts`. **Never** hit `fetch` directly from a
  component.
- Mock data lives in `src/features/<domain>/api/_mocks.ts`. Flip
  `NEXT_PUBLIC_USE_MOCKS=1` to force mock mode in dev.

## File layout rules

```
src/
  app/                          # Next App Router — thin route shells only
    (authenticated)/
      layout.tsx                # AppShell
      page.tsx                  # Dashboard
      agents/[id]/page.tsx      # Agent detail
      workflows/[id]/page.tsx   # Workflow detail
      marketplace/page.tsx
      marketplace/[id]/page.tsx
  components/
    ui/                         # shadcn primitives — DO NOT hand-edit without
                                # also updating the matching entry in
                                # design-system/components.html
    brand/                      # Logo, etc.
    feedback/                   # Empty/Error/Loading states, page-loader
    icons/                      # re-exports of lucide + custom SVGs
  features/
    agents/  workflows/  marketplace/  chat/  layout/  search/  workspace/
      api/
      components/
      hooks/
      lib/
      providers/  (if needed)
  hooks/                        # cross-cutting
  lib/                          # http, auth, ws, utils, constants
  types/
```

**Rule**: if a component is used by more than one feature, it lives in
`components/`. If only by one feature, it lives in `features/<name>/components/`.

## Commands

- `npm run dev` — Next dev on :3000
- `npm run build && npm run start` — prod
- `npm run lint`

## When implementing a new screen

1. Open `/design-system/screens.md` and find the screen spec.
2. Open `/design-system/components.html` in a browser (it's a static page)
   for live component reference + shadcn mapping table.
3. Compose from `components/ui/*` primitives first. Only add new primitives
   when the same pattern appears 3+ times.
4. Pull data through a feature API client in mock mode. Don't block on
   backend readiness.
5. Run `npm run lint` before committing.

## When the user says something ambiguous

Ask. This codebase is the product of a very specific design vision — guessing
will drift the visual language. The design system doc is the tiebreaker.
