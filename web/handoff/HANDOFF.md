# Aviary — Aurora Glass → Aviary Slate Handoff

Migration plan from the current "Aurora Glass" design to the new
"Aviary Slate" design system. Hand this entire `handoff/` directory to
Claude Code, point it at the repo root, and ask it to follow this doc.

---

## TL;DR

- **Design system is changing wholesale.** Every visual decision (palette,
  type, radius, motion, component shapes) is being replaced. Layout and
  information architecture are also being redesigned per screen.
- **Stack is not changing.** Next.js 15 + React 19 + Tailwind + shadcn stays.
- **Folder structure is largely preserved**, with surgical additions to
  `features/*` to match the new screens (workspace file tree, workflow
  builder inspector, command palette sections, etc.).
- **Aurora Glass assets are deleted**, not kept as an alt theme.

---

## Package contents

```
handoff/
├── CLAUDE.md                        → copy to repo root
├── HANDOFF.md                       → this file
├── design-system/
│   ├── index.html                   open in browser — live reference
│   ├── tokens.md                    every CSS variable, light + dark
│   ├── typography.md                type scale + examples
│   ├── components.html              live component catalog + shadcn map
│   └── screens.md                   per-screen layout specs
├── code/
│   ├── app/
│   │   ├── globals.css              → src/app/globals.css (REPLACE)
│   │   └── fonts.ts                 → src/app/fonts.ts (REPLACE)
│   ├── tailwind.config.ts           → web/tailwind.config.ts (REPLACE)
│   ├── components/ui/               → src/components/ui/* (REPLACE each)
│   ├── features/layout/
│   │   ├── route.ts                 → src/features/layout/lib/route.ts (NEW)
│   │   ├── route-provider.tsx       → src/features/layout/providers/ (NEW)
│   │   └── app-shell.tsx            → src/features/layout/components/ (REPLACE)
│   ├── types/                       → src/types/* (REPLACE mock types)
│   └── mocks/                       → src/features/*/api/_mocks.ts (NEW)
└── reference-prototype.zip          the HTML prototype, self-contained
```

---

## Migration order (follow this sequence)

### Phase 0 — Prep (10 min)

1. Create a branch: `git checkout -b design-system/aviary-slate`.
2. Copy `handoff/CLAUDE.md` → repo root `CLAUDE.md`.
3. Read `design-system/index.html` in a browser. Keep it open as reference.
4. Verify the reference prototype by unzipping `reference-prototype.zip` and
   opening `index.html` — this is the visual target.

### Phase 1 — Strip Aurora Glass (30 min)

Delete these files completely:

```
src/components/brand/aurora-backdrop.tsx
web/DESIGN_NOTES_AURORA_GLASS.md
```

Search-and-purge these symbols across the codebase:

- `AuroraBackdrop` — component is gone
- `aurora-a`, `aurora-b`, `aurora-c` — gradient tokens gone
- `aurora-sheen` animation — gone
- `glass-pane`, `glass-raised`, `glass-deep` — classes gone
- `--bg-canvas: #08091A` and any other Aurora color tokens — replaced
- `backdrop-filter: blur(…) saturate(…)` — remove, no glass anymore
- References in `layout.tsx` to `<AuroraBackdrop />`

Expected result: the app visually breaks. That's fine — Phase 2 rebuilds.

### Phase 2 — Tokens, Tailwind, fonts (20 min)

Replace wholesale:

1. `src/app/globals.css` ← `handoff/code/app/globals.css`
2. `tailwind.config.ts` ← `handoff/code/tailwind.config.ts`
3. `src/app/fonts.ts` ← `handoff/code/app/fonts.ts` (Inter + JetBrains Mono only)

Add to `src/app/layout.tsx`:

```tsx
<html lang="en" data-theme="dark" suppressHydrationWarning>
  <body className={`${inter.variable} ${jetbrainsMono.variable}`}>
    {children}
  </body>
</html>
```

Theme toggle logic moves into the user menu (see `app-shell.tsx`).

### Phase 3 — shadcn primitives (45 min)

Replace each file in `src/components/ui/` with the matching file in
`handoff/code/components/ui/`. The public API (props, variants) is preserved
where possible — most callers won't need to change. New primitives to add:

- `avatar.tsx` — tone-based identity tile (NEW)
- `status-dot.tsx` — colored dot with soft halo (NEW)
- `kind-badge.tsx` — private / published / imported badge (NEW)

### Phase 4 — Route provider + app shell (60 min)

1. Add `src/features/layout/lib/route.ts` — the Route type + defaults.
2. Add `src/features/layout/providers/route-provider.tsx` — Context provider
   with `useRoute()` hook and URL sync effect.
3. Replace `src/app/(authenticated)/layout.tsx` with the new `AppShell` that
   uses `useRoute` for sidebar highlighting, search palette, notifications,
   and user menu.
4. Wire `app/(authenticated)/page.tsx`, `.../agents/[id]/page.tsx`, etc. to
   read URL params once on mount and call `setRoute(...)`.

### Phase 5 — Screens, one at a time (~2 days)

Follow `design-system/screens.md`. Build in this order — each unblocks the
next:

1. **Dashboard** — stats row, recent chat sessions, recent workflow runs,
   published asset reach block
2. **Agents list** — Featured, Category filter, kind tabs
3. **Agent detail / chat** — 3-pane: session list · chat thread · workspace
4. **Agent detail / workspace** — file tree → click opens Monaco editor
5. **Agent editor** — prompt, tools, model settings
6. **Workflows list** — mirrors Agents list shape
7. **Workflow detail** — runs list, stats, last-run summary
8. **Workflow builder** — xyflow canvas, right = per-node inspector or
   live run stream, bottom = AI assistant
9. **Marketplace list** — filters (category, published-by-me), kind tabs
10. **Marketplace item** — detail, install button, version history

### Phase 6 — Real backend (ongoing)

Every feature API client ships in mock mode. To cut over, implement the
real fetch in the same file — the component contract stays unchanged.
Search for `// TODO: real endpoint` markers.

### Phase 7 — Polish (ongoing)

- Command palette (⌘K) — sections: Agents, Workflows, Sessions (full-text)
- Notifications panel — chat replies, workflow completions, failures
- Empty / error / loading states per feature
- `prefers-reduced-motion` honored in all transitions (tokens already do this)

---

## Mock → Real wiring points

| Surface | Mock file | Swap to |
|---|---|---|
| Agents list | `features/agents/api/_mocks.ts` | `GET /api/agents` |
| Agent detail | same | `GET /api/agents/:id` |
| Agent chat | `features/chat/api/_mocks.ts` | WebSocket `wss://…/chat/:sessionId` |
| Workspace tree | `features/workspace/api/_mocks.ts` | `GET /api/agents/:id/workspace/tree` |
| File contents | same | `GET /api/agents/:id/workspace/file?path=…` |
| Workflows | `features/workflows/api/_mocks.ts` | `GET /api/workflows` |
| Workflow runs | same | WebSocket for live runs |
| Marketplace | `features/marketplace/api/_mocks.ts` | `GET /api/marketplace` |
| Sessions search | `features/search/api/_mocks.ts` | `GET /api/search?q=…` |
| Notifications | `features/layout/api/_mocks.ts` | `GET /api/notifications` (SSE preferred) |

The existing `src/lib/http/client.ts` and `src/lib/ws/*` utilities are kept
as-is. Only the endpoint URLs change.

---

## Specific behaviors to preserve

These are encoded in the prototype — Claude Code, don't lose them:

1. **Tone is identity.** Every agent and workflow record carries a `tone`
   field (`blue | green | amber | pink | purple | teal | rose | slate`).
   It's picked once on creation and never changes. Used for the avatar tile
   and the soft accent on chat bubbles, run cards, notification dots.

2. **Three ownership states**: `kind: "private" | "published" | "imported"`.
   `published` records gain `installs: number`. `imported` records gain
   `author: string` and `hasUpdate?: boolean`.

3. **Command palette sections**: Agents, Workflows, Sessions. Sessions is
   **full-text across message snippets**, not just titles. Highlights the
   matched substring inline using `<mark>` styled with accent soft bg.

4. **Notifications** are user-facing events, not system telemetry:
   - `chat_reply` — an agent finished responding in a session you own
   - `workflow_complete` / `workflow_failed` — a run you triggered finished
   Clicking a notification navigates to that session or run.

5. **Workflow builder layout**: canvas center, **right panel** is
   context-sensitive (shows selected-node inspector OR live run stream if
   a run is active), **bottom panel** is the AI assistant. Do not revert
   to the old "AI assistant on the right" layout.

6. **Agent chat workspace**: file tree on far right, clicking a file opens
   a Monaco editor as an *overlay panel* sliding in from the right, pushing
   the tree to be a narrow column. Two-tier layout, not a modal.

7. **Dashboard hero numbers** are: chat sessions this week, workflow runs
   this week, published agents (with install sum), published workflows
   (with install sum). **Not** "agent runs" or "tokens used" — those
   were removed in redesign.

8. **Marketplace cards** do not show author. Author appears only on the
   detail page. List-view rows also hide author.

9. **Header user pill** shows only the MK avatar circle. Name/email appear
   only in the dropdown.

10. **Theme persistence**: `data-theme` on `<html>`, persisted to
    `localStorage.aviary-theme`. Default dark.

---

## What NOT to do

- Don't re-introduce glass / backdrop-filter — it's gone on purpose.
- Don't use gradients on UI chrome (tone avatars use solid tinted fills).
- Don't hand-edit `components/ui/*` without also updating the matching
  entry in `design-system/components.html`. The doc is the spec.
- Don't inline hex values. All colors come from CSS variables.
- Don't add a "landing page" or marketing surface. This is a tool, not a
  site.
- Don't replace `lucide-react` icons with emoji or custom icons unless
  lucide genuinely lacks one.

---

## Checklist before the design PR merges

- [ ] `AuroraBackdrop` and all aurora-* tokens are gone from the codebase
- [ ] `npm run build` passes
- [ ] `npm run lint` passes
- [ ] All 10 screens in `screens.md` are implemented
- [ ] Light + dark themes both render correctly on every screen
- [ ] No hardcoded hex colors outside `globals.css` (grep for `#[0-9A-F]`)
- [ ] Every number uses tabular-nums
- [ ] Command palette full-text search works
- [ ] Notifications click-through navigates correctly
- [ ] `prefers-reduced-motion` disables animations

---

Questions for the humans, not Claude Code:

- Is Aurora Glass truly gone, or is it worth keeping as a theme option
  behind a feature flag? (Current plan: gone.)
- Which real endpoints exist today and which need to be built? (Fill in
  the "Mock → Real wiring points" table.)
- What's the plan for i18n? The prototype is Korean + English mixed. The
  current code has no i18n system; if we need one, do it before Phase 5.
