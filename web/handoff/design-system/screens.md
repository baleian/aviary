# Aviary Slate — Screen Specs

One section per primary screen. For each: purpose, layout, data shape,
and the components to compose from. The reference prototype (`index.html`
in this handoff) is the visual ground truth — open it alongside.

All screens share the same **AppShell**: 220px collapsible sidebar + 48px
header + main area. Header contains the breadcrumb, search pill (⌘K),
notifications bell, user avatar.

---

## 1. Dashboard — `primary: "dashboard"`

**Purpose**: At-a-glance summary of what the user has been doing and what
their published assets are doing in the wild.

**Layout** (main area, top-to-bottom):

```
┌─────────────────────────────────────────────────────────────┐
│ Page header                                                 │
│   Greeting ("안녕하세요, 민수") + date                        │
│   Right: [+ New agent] [+ New workflow]                     │
├─────────────────────────────────────────────────────────────┤
│ Stat row — 4 cards, equal width                             │
│   Chat sessions │ Workflow runs │ Published agents │ …      │
│   (this week)   │ (this week)   │ (count + Σinstalls)│ …    │
├─────────────────────────────────────────────────────────────┤
│ Two-column split                                            │
│ ┌──────────────────────────┬──────────────────────────────┐ │
│ │ Recent chat sessions     │ Recent workflow runs         │ │
│ │ 5-row list               │ 5-row list                   │ │
│ │ (agent tone avatar,      │ (workflow tone avatar,       │ │
│ │  title, preview, time,   │  trigger mono, status dot,   │ │
│ │  msg count)              │  duration, time)             │ │
│ └──────────────────────────┴──────────────────────────────┘ │
├─────────────────────────────────────────────────────────────┤
│ Reach block — horizontal strip                              │
│   "Your published work" — for each published asset:         │
│   tone avatar, name, kind badge, version, Σinstalls num     │
└─────────────────────────────────────────────────────────────┘
```

**Removed from old design**: agent runs graph, tokens-used meter, sparklines,
this-week activity timeline. Don't re-add.

**Data**:
```ts
Dashboard.Data = {
  stats: { chatSessions, workflowRuns, publishedAgents, publishedWorkflows };
  recentSessions: Session[5];
  recentRuns: Run[5];
  reach: (Agent | Workflow)[] where kind === "published";
}
```

**Components**: `StatCard`, `SessionRow`, `RunRow`, `ReachStrip`.

---

## 2. Agents list — `primary: "agents"`

**Purpose**: Browse & jump into any agent.

**Layout**:

```
┌─────────────────────────────────────────────────────────────┐
│ Header: "Agents"  + [New agent]                             │
├─────────────────────────────────────────────────────────────┤
│ Kind tabs: All · Private · Published · Imported             │
│ Right: Search (inline) · View toggle (grid/list)            │
├─────────────────────────────────────────────────────────────┤
│ FEATURED strip — 3 big cards (only Published by me + recent)│
│                                                             │
│ All · Category pills (Dev Tools, Infra, Security, …)       │
│                                                             │
│ Grid — AgentCard × N, 3-col responsive                      │
│   ┌─────────────────────────────┐                           │
│   │ [tone avatar]  PR Reviewer  │                           │
│   │                [•private]   │                           │
│   │ GitHub PR을 읽고…           │                           │
│   │ ─────────────────────────── │                           │
│   │ 6 tools · 14 sessions       │                           │
│   │                Claude Sonnet│                           │
│   └─────────────────────────────┘                           │
└─────────────────────────────────────────────────────────────┘
```

**Card states**:
- Private: no badge, `--badge-private-*`
- Published: "Published" badge + install count at bottom-right
- Imported: "Imported" badge + `@author` in metadata + update dot if
  `hasUpdate`

**Click behavior**: `setRoute({ primary: "agent", agentId: a.id, agentTab: "chat" })`

**Components**: `AgentCard`, `CategoryPills`, `KindTabs`, `FeaturedStrip`.

---

## 3. Agent detail — `primary: "agent"`

Three tabs. Default = `chat`.

### 3a. Chat tab (`agentTab: "chat"`)

**Layout** — 3 panes:

```
┌────────────┬───────────────────────────┬──────────────────┐
│ Sessions   │ Chat thread               │ Workspace        │
│ (240px)    │ (flex)                    │ (340px, resizes) │
│            │                           │                  │
│ [+ New]    │ ┌─ message ─ MK ────────┐ │ ▸ src           │
│            │ │ …                     │ │   ▸ features    │
│ • Pinned   │ └───────────────────────┘ │     ▼ auth      │
│   - title  │ ┌─ message ─ [PR Rev] ──┐ │       • token…  │
│   - title  │ │ …                     │ │       • session…│
│            │ │ ▸ tool: read_file     │ │   ▸ billing    │
│ • Today    │ │ ▸ tool: edit_file     │ │ ▸ tests         │
│   - title  │ └───────────────────────┘ │ • package.json  │
│   - title  │                           │                  │
│ • Earlier  │ ┌─ composer ────────────┐ │                  │
│   - …      │ │ Message PR Reviewer…  │ │                  │
│            │ └───────────────────────┘ │                  │
└────────────┴───────────────────────────┴──────────────────┘
```

**Message shape**: role (user/assistant/system), content (markdown),
tool calls (collapsed by default, expand inline). Uses `react-markdown` +
`remark-gfm` + `rehype-highlight`.

**Tool call display**: `▸ tool_name { args }` — click to expand input/output
diff using `react-diff-viewer-continued`.

### 3b. Workspace overlay

Clicking a file in the tree **doesn't navigate** — it opens a Monaco editor
as an overlay panel that slides in from the right, pushing the tree to a
narrow column (220px). Close button returns to the 3-pane chat layout.

```
┌────────────┬──────────────────────────┬──────┬────────────┐
│ Sessions   │ Chat thread              │ Tree │ Editor     │
│            │                          │ 220  │ (flex)     │
│ (same as   │ (dimmed to 70% when      │      │ Monaco     │
│  chat tab) │  editor is open)         │      │            │
└────────────┴──────────────────────────┴──────┴────────────┘
```

### 3c. Editor tab (`agentTab: "editor"`)

Agent definition editor — prompt, tools, model, parameters. Two columns:
form on left (560px), live preview/test on right.

### 3d. Runs tab (`agentTab: "runs"`) · Settings tab (`agentTab: "settings"`)

Standard list views, matching the patterns in Workflows.

---

## 4. Workflows list — `primary: "workflows"`

Mirrors Agents list shape: kind tabs · featured strip · category pills · card grid.

**WorkflowCard** differs from AgentCard: shows node count, last run status
dot, last run timestamp instead of tool count / session count.

---

## 5. Workflow detail — `primary: "workflow"`

Tabs: overview · builder · runs · settings. Default = `overview`.

### 5a. Overview

```
┌─────────────────────────────────────────┬─────────────────┐
│ Header: name, kind badge, version       │ Right sidebar   │
│ Description                             │                 │
├─────────────────────────────────────────┤ Recent runs     │
│ Stat row: Total runs · Last status ·    │ (7-row list,    │
│           Avg duration · Success rate   │  click = open   │
├─────────────────────────────────────────┤  run detail)    │
│ Node graph thumbnail (readonly xyflow)  │                 │
├─────────────────────────────────────────┤                 │
│ Triggers: webhook / tag / schedule      │                 │
└─────────────────────────────────────────┴─────────────────┘
```

### 5b. Builder (`workflowTab: "builder"`)

```
┌─────────────────────────────────────────┬─────────────────┐
│ Node palette (left, 200px, collapsible) │                 │
├─────────────────────────────────────────┤ Inspector       │
│                                         │   (340px)       │
│   xyflow canvas                         │                 │
│   dark grid, tone-blue edges,           │ Selected node:  │
│   node = rounded 10px card              │  - config form  │
│                                         │  - inputs/outputs│
│                                         │                 │
│                                         │ OR if run live: │
│                                         │  - live stream  │
│                                         │  - node statuses│
├─────────────────────────────────────────┴─────────────────┤
│ AI Assistant bottom panel (collapsible, 260px when open)  │
│ "Describe the change you want…"                           │
└───────────────────────────────────────────────────────────┘
```

**Critical**: right panel is Inspector **OR** live-run stream (not both
simultaneously). AI assistant lives at the **bottom**, not the right.

### 5c. Runs (`workflowTab: "runs"`)

Filterable list, click a row → run detail with full execution trace,
node-by-node input/output, timing waterfall.

---

## 6. Marketplace list — `primary: "marketplace"`

```
┌─────────────────────────────────────────────────────────────┐
│ Header: "Marketplace"                                       │
├─────────────────────────────────────────────────────────────┤
│ Tabs: All · Agents · Workflows · Published by me            │
│ Right: Search · Category filter · Sort (Installs / Recent)  │
├─────────────────────────────────────────────────────────────┤
│ FEATURED — 3 cards (curated, high-install)                  │
├─────────────────────────────────────────────────────────────┤
│ Grid — MarketplaceCard × N, 3-col                           │
│   ┌─────────────────────────────┐                           │
│   │ [tone avatar]   name        │                           │
│   │                 v3.1.0 ●new │  ← "new update" dot       │
│   │ desc                        │                           │
│   │ ─────────────────────────── │                           │
│   │ ⭐ 4.8  ↓ 1.2k installs    │                           │
│   └─────────────────────────────┘                           │
│   (NO author on card)                                       │
└─────────────────────────────────────────────────────────────┘
```

---

## 7. Marketplace item — `primary: "marketplace-item"`

Two columns: left = description / changelog / README (markdown), right =
sidebar with [Install] CTA, version picker, author card, install count,
rating, categories.

---

## Cross-cutting overlays

### Command palette (⌘K)

```
┌─────────────────────────────────────────┐
│ 🔍 Agents · Workflows · Sessions 검색…  │
├─────────────────────────────────────────┤
│ AGENTS                                3 │
│  [tone] PR Reviewer                     │
│  [tone] SQL Explainer                   │
│                                         │
│ WORKFLOWS                             2 │
│  [tone] Release Notes Pipeline          │
│                                         │
│ SESSIONS                              4 │
│  [tone] feat/auth-refactor PR 리뷰      │
│         …token-validator.ts:42 — …      │
│         PR Reviewer · 12m ago           │
├─────────────────────────────────────────┤
│ ↑ ↓ 이동  ↵ 선택            Aviary Search │
└─────────────────────────────────────────┘
```

Sessions section does **full-text match on message snippets** and shows
the matched substring highlighted inline with accent-soft bg.

### Notifications dropdown

Triggered by bell icon in header. 360px panel. User-facing events only:

- `chat_reply` — agent finished responding
- `workflow_complete` — your workflow run succeeded
- `workflow_failed` — your workflow run failed

Each row: tone avatar · title · desc · timestamp · unread dot. Clicking
navigates to the relevant session or run.

### User menu

Triggered by avatar in header. 260px panel. Sections:
- User card (avatar, name, email)
- Profile & preferences · API keys · Model defaults · Teams & access
- Appearance (Light / Dark radio)
- Sign out

---

## Responsive behavior

This is a desktop tool. Minimum supported width: **1280px**.
Below that: show a "best viewed on a larger screen" notice. Don't
collapse into a mobile layout — the density model doesn't survive.

Sidebar collapse (220 → 56) at `< 1440px` is automatic; user can override.
