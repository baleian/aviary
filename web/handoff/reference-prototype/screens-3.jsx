/* global React, Icons, Shell, Data */
const { useState: useStateS3 } = React;
const hS3 = React.createElement;
const { KindBadge } = Shell;

/* ============================================================
 * WORKFLOWS MAIN
 * ============================================================ */
function WorkflowsScreen({ onRoute }) {
  const [filter, setFilter] = useStateS3("all");
  const [cat, setCat] = useStateS3("All");
  const [q, setQ] = useStateS3("");

  const filtered = Data.workflows.filter(w => {
    if (filter !== "all" && w.kind !== filter) return false;
    if (cat !== "All" && w.category !== cat) return false;
    if (q && !(w.name + " " + w.desc).toLowerCase().includes(q.toLowerCase())) return false;
    return true;
  });

  const featured = Data.workflows.filter(w => w.featured);

  const counts = {
    all: Data.workflows.length,
    private: Data.workflows.filter(w => w.kind === "private").length,
    published: Data.workflows.filter(w => w.kind === "published").length,
    imported: Data.workflows.filter(w => w.kind === "imported").length,
  };

  // Categories: from workflow data
  const allCats = Array.from(new Set(Data.workflows.map(w => w.category))).sort();
  const cats = ["All", ...allCats];

  return hS3("div", { style: { height: "100%", overflowY: "auto" } },
    hS3("div", { style: { padding: "20px 32px 0" } },
      hS3("div", { style: { maxWidth: 1400, margin: "0 auto" } },
        hS3("div", { style: { display: "flex", alignItems: "flex-end", justifyContent: "space-between", marginBottom: 18 } },
          hS3("div", null,
            hS3("div", { className: "t-hero" }, "Workflows"),
            hS3("div", { className: "t-small fg-tertiary", style: { marginTop: 2 } }, "결정·분기·호출을 묶어 반복 가능한 파이프라인으로 만듭니다.")
          ),
          hS3("div", { style: { display: "flex", gap: 8 } },
            hS3("button", { className: "btn btn-outline btn-sm" }, hS3(Icons.Upload, { size: 13 }), "Import"),
            hS3("button", { className: "btn btn-primary btn-sm" }, hS3(Icons.Plus, { size: 13 }), "New Workflow")
          )
        ),

        // Featured row — only when All + no search
        filter === "all" && cat === "All" && !q && featured.length > 0 && hS3("div", { style: { marginBottom: 20 } },
          hS3("div", { style: { display: "flex", alignItems: "center", gap: 8, marginBottom: 10 } },
            hS3("div", { className: "t-over" }, "Featured"),
            hS3("span", { style: { height: 1, flex: 1, background: "var(--border-subtle)" } })
          ),
          hS3("div", { style: { display: "grid", gridTemplateColumns: `repeat(${Math.min(featured.length, 3)}, 1fr)`, gap: 12 } },
            featured.slice(0, 3).map(w => hS3(FeaturedWorkflowCard, { key: w.id, wf: w, onClick: () => onRoute({ primary: "workflow", workflowId: w.id }) }))
          )
        ),

        // Filter tabs + controls (match Agents layout)
        hS3("div", {
          style: {
            display: "flex", alignItems: "center", gap: 12,
            padding: "10px 0", marginBottom: 10,
            borderTop: "1px solid var(--border-subtle)",
            borderBottom: "1px solid var(--border-subtle)",
            position: "sticky", top: 0, background: "var(--bg-base)", zIndex: 2
          }
        },
          // kind tabs
          hS3("div", { style: { display: "flex", gap: 2 } },
            [
              { id: "all", label: "All", icon: null },
              { id: "private", label: "Private", icon: Icons.Lock },
              { id: "published", label: "Published", icon: Icons.Upload },
              { id: "imported", label: "Imported", icon: Icons.Download },
            ].map(tab => {
              const active = filter === tab.id;
              return hS3("button", {
                key: tab.id,
                onClick: () => setFilter(tab.id),
                style: {
                  display: "flex", alignItems: "center", gap: 6,
                  padding: "5px 10px", borderRadius: 6, fontSize: 12.5, fontWeight: 500,
                  background: active ? "var(--bg-raised)" : "transparent",
                  color: active ? "var(--fg-primary)" : "var(--fg-tertiary)",
                  border: active ? "1px solid var(--border-default)" : "1px solid transparent",
                }
              },
                tab.icon && hS3(tab.icon, { size: 12 }),
                hS3("span", null, tab.label),
                hS3("span", {
                  style: {
                    fontSize: 10.5, padding: "1px 6px", borderRadius: 99,
                    background: active ? "var(--accent-blue-soft)" : "var(--bg-sunk)",
                    color: active ? "var(--accent-blue)" : "var(--fg-muted)",
                    fontVariantNumeric: "tabular-nums", marginLeft: 2,
                  }
                }, counts[tab.id])
              );
            })
          ),

          hS3("div", { style: { height: 16, width: 1, background: "var(--border-subtle)" } }),

          // Category filter
          hS3("div", { style: { display: "flex", alignItems: "center", gap: 6 } },
            hS3("span", { style: { fontSize: 11, color: "var(--fg-muted)", fontWeight: 500 } }, "Category"),
            hS3("select", {
              value: cat, onChange: e => setCat(e.target.value),
              style: {
                padding: "4px 22px 4px 8px", borderRadius: 6, fontSize: 12,
                background: "var(--bg-raised)", border: "1px solid var(--border-default)",
                color: "var(--fg-primary)", fontWeight: 500, cursor: "pointer",
                appearance: "none",
                backgroundImage: `url("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='10' height='10' viewBox='0 0 24 24' fill='none' stroke='%238a8f98' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'><polyline points='6 9 12 15 18 9'/></svg>")`,
                backgroundRepeat: "no-repeat", backgroundPosition: "right 6px center",
              }
            },
              cats.map(c => hS3("option", { key: c, value: c }, c + (c === "All" ? "" : ` (${Data.workflows.filter(w=>w.category===c).length})`)))
            )
          ),

          hS3("div", { style: { flex: 1 } }),

          // Search
          hS3("div", { style: { position: "relative" } },
            hS3(Icons.Search, { size: 13, style: { position: "absolute", left: 10, top: "50%", transform: "translateY(-50%)", color: "var(--fg-muted)" } }),
            hS3("input", {
              className: "input",
              value: q, onChange: e => setQ(e.target.value),
              placeholder: "Filter workflows…",
              style: { width: 220, paddingLeft: 30, height: 28, fontSize: 12 }
            })
          )
        )
      )
    ),
    hS3("div", { style: { padding: "4px 32px 40px" } },
      hS3("div", { style: { maxWidth: 1400, margin: "0 auto" } },
        filtered.length === 0
          ? hS3("div", { style: { padding: "60px 20px", textAlign: "center", color: "var(--fg-muted)", fontSize: 13 } },
              "조건과 일치하는 워크플로우가 없습니다."
            )
          : hS3("div", { style: { display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(320px, 1fr))", gap: 12 } },
              filtered.map(w => hS3(WorkflowCard, { key: w.id, wf: w, onClick: () => onRoute({ primary: "workflow", workflowId: w.id }) }))
            )
      )
    )
  );
}

// Featured workflow card — more visual than standard card
function FeaturedWorkflowCard({ wf, onClick }) {
  return hS3("button", {
    onClick,
    className: "card card-hover",
    style: {
      padding: 0, textAlign: "left", display: "flex", flexDirection: "column",
      cursor: "pointer", overflow: "hidden", position: "relative",
    }
  },
    // Top band with tone gradient — reads the tone class background color
    hS3("div", {
      className: `tone-${wf.tone}`,
      style: {
        height: 72, position: "relative",
        borderBottom: "1px solid var(--border-subtle)",
        display: "flex", alignItems: "center", padding: "0 14px",
        // override avatar color-on-bg from .tone-* so the band looks tinted not solid-text
        color: "inherit",
      }
    },
      hS3("div", {
        className: `avatar tone-${wf.tone}`,
        style: {
          width: 40, height: 40, borderRadius: 10,
          background: "var(--bg-raised)", // solid chip on the tinted band
          boxShadow: "var(--shadow-sm)",
        }
      },
        hS3(Icons.Workflows, { size: 18 })
      ),
      hS3("div", { style: { marginLeft: 12, flex: 1, minWidth: 0 } },
        hS3("div", { style: { fontSize: 14, fontWeight: 600 }, className: "truncate" }, wf.name),
        hS3("div", { style: { display: "flex", alignItems: "center", gap: 6, fontSize: 11, color: "var(--fg-tertiary)", marginTop: 2 } },
          hS3(KindBadge, { kind: wf.kind }),
          hS3("span", null, "·"),
          hS3("span", null, wf.category)
        )
      ),
      hS3("span", {
        style: {
          position: "absolute", top: 10, right: 10,
          fontSize: 10, fontWeight: 600, letterSpacing: "0.06em",
          padding: "2px 7px", borderRadius: 99,
          background: "var(--accent-blue)", color: "white",
          textTransform: "uppercase",
        }
      }, "Featured")
    ),
    hS3("div", { style: { padding: "10px 14px 12px", display: "flex", flexDirection: "column", gap: 8 } },
      hS3("div", { style: { fontSize: 12.5, color: "var(--fg-secondary)", lineHeight: 1.5, minHeight: 36, display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden" } }, wf.desc),
      hS3("div", { style: { display: "flex", gap: 14, fontSize: 11.5, color: "var(--fg-tertiary)" } },
        hS3("span", null, hS3("span", { className: "t-mono", style: { fontVariantNumeric: "tabular-nums", fontWeight: 600, color: "var(--fg-secondary)" } }, wf.nodes), " nodes"),
        hS3("span", null, hS3("span", { className: "t-mono", style: { fontVariantNumeric: "tabular-nums", fontWeight: 600, color: "var(--fg-secondary)" } }, wf.runs), " runs"),
        wf.installs && hS3("span", null, hS3("span", { className: "t-mono", style: { fontVariantNumeric: "tabular-nums", fontWeight: 600, color: "var(--fg-secondary)" } }, wf.installs), " imports"),
        hS3("span", { style: { flex: 1 } }),
        hS3("span", {
          style: {
            color: wf.lastStatus === "failed" ? "var(--status-error)" :
                   wf.lastStatus === "completed" ? "var(--status-live)" : "var(--fg-muted)"
          }
        }, wf.lastRun)
      )
    )
  );
}

function WorkflowCard({ wf, onClick }) {
  return hS3("button", {
    onClick, className: "card card-hover",
    style: { padding: 14, textAlign: "left", display: "flex", flexDirection: "column", gap: 10, cursor: "pointer" }
  },
    hS3("div", { style: { display: "flex", alignItems: "flex-start", gap: 10 } },
      hS3("div", { className: `avatar tone-${wf.tone}`, style: { width: 32, height: 32 } },
        hS3(Icons.Workflows, { size: 15 })
      ),
      hS3("div", { style: { flex: 1, minWidth: 0 } },
        hS3("div", { style: { fontSize: 14, fontWeight: 600 } }, wf.name),
        hS3("div", { style: { display: "flex", alignItems: "center", gap: 6, fontSize: 11.5, color: "var(--fg-tertiary)" } },
          hS3(KindBadge, { kind: wf.kind }),
          wf.version && hS3("span", { className: "t-mono", style: { fontSize: 11 } }, wf.version),
          hS3("span", {
            className: "chip",
            style: {
              height: 18, background: wf.status === "deployed" ? "var(--status-live-soft)" : "var(--status-warn-soft)",
              color: wf.status === "deployed" ? "var(--status-live)" : "var(--status-warn)"
            }
          }, wf.status)
        )
      )
    ),
    hS3("div", { style: { fontSize: 12.5, color: "var(--fg-secondary)", minHeight: 36, display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden" } }, wf.desc),
    hS3("div", { className: "sep-h" }),
    hS3("div", { style: { display: "flex", gap: 14, fontSize: 11.5, color: "var(--fg-tertiary)" } },
      hS3("span", null, hS3("span", { className: "t-mono", style: { fontVariantNumeric: "tabular-nums" } }, wf.nodes), " nodes"),
      hS3("span", null, hS3("span", { className: "t-mono", style: { fontVariantNumeric: "tabular-nums" } }, wf.runs), " runs"),
      hS3("span", { style: { flex: 1 } }),
      hS3("span", {
        style: {
          color: wf.lastStatus === "failed" ? "var(--status-error)" :
                 wf.lastStatus === "completed" ? "var(--status-live)" : "var(--fg-muted)"
        }
      }, wf.lastStatus + " · " + wf.lastRun)
    )
  );
}

/* ============================================================
 * WORKFLOW DETAIL — runs list sidebar + builder preview
 * ============================================================ */
function WorkflowDetailScreen({ workflowId, onRoute }) {
  const wf = Data.workflows.find(w => w.id === workflowId) || Data.workflows[0];
  const [activeRun, setActiveRun] = useStateS3(Data.workflowRuns[0].id);
  const [mode, setMode] = useStateS3("runs"); // runs | builder

  return hS3("div", { style: { display: "flex", height: "100%", minHeight: 0 } },
    // Sub-sidebar: runs for THIS workflow
    hS3("aside", {
      style: {
        width: 280, background: "var(--bg-surface)", borderRight: "1px solid var(--border-subtle)",
        display: "flex", flexDirection: "column", flexShrink: 0
      }
    },
      hS3("div", { style: { padding: "14px 14px 12px", borderBottom: "1px solid var(--border-subtle)" } },
        hS3("div", { style: { display: "flex", alignItems: "center", gap: 10, marginBottom: 10 } },
          hS3("button", { onClick: () => onRoute({ primary: "workflows" }), className: "btn btn-ghost btn-icon btn-sm" }, hS3(Icons.ChevronLeft, { size: 14 })),
          hS3("div", { className: `avatar tone-${wf.tone}`, style: { width: 30, height: 30 } }, hS3(Icons.Workflows, { size: 14 })),
          hS3("div", { style: { flex: 1, minWidth: 0 } },
            hS3("div", { style: { fontSize: 13, fontWeight: 600 }, className: "truncate" }, wf.name),
            hS3("div", { style: { fontSize: 11, color: "var(--fg-tertiary)" } }, wf.nodes + " nodes · " + wf.runs + " runs")
          )
        ),
        hS3("button", { className: "btn btn-primary btn-sm", style: { width: "100%", justifyContent: "center" } },
          hS3(Icons.Play, { size: 12 }), "Trigger run"
        )
      ),
      hS3("div", { style: { padding: "8px", flex: 1, overflowY: "auto" } },
        hS3("div", { className: "t-over", style: { padding: "6px 8px" } }, "Recent runs"),
        Data.workflowRuns.map(r => {
          const isActive = r.id === activeRun;
          const dotColor = r.status === "running" ? "var(--status-info)" :
                           r.status === "completed" ? "var(--status-live)" :
                           r.status === "failed" ? "var(--status-error)" : "var(--fg-muted)";
          return hS3("button", {
            key: r.id, onClick: () => setActiveRun(r.id),
            style: {
              width: "100%", textAlign: "left",
              padding: "8px 10px", borderRadius: 7, marginBottom: 1,
              background: isActive ? "var(--bg-active)" : "transparent",
              display: "flex", gap: 8, alignItems: "flex-start"
            },
            onMouseOver: e => { if (!isActive) e.currentTarget.style.background = "var(--bg-hover)"; },
            onMouseOut:  e => { if (!isActive) e.currentTarget.style.background = "transparent"; }
          },
            hS3("span", { className: "dot", style: { background: dotColor, marginTop: 6, flexShrink: 0 } }),
            hS3("div", { style: { flex: 1, minWidth: 0 } },
              hS3("div", { style: { fontSize: 12.5, fontWeight: 500 }, className: "truncate" }, r.id),
              hS3("div", { className: "t-mono truncate", style: { fontSize: 10.5, color: "var(--fg-tertiary)" } }, r.trigger),
              hS3("div", { style: { fontSize: 10.5, color: "var(--fg-muted)", marginTop: 2, fontVariantNumeric: "tabular-nums" } }, r.when + " · " + r.duration)
            )
          );
        })
      )
    ),

    // Main
    hS3("div", { style: { flex: 1, display: "flex", flexDirection: "column", minWidth: 0 } },
      hS3("div", { style: { height: 44, padding: "0 16px", borderBottom: "1px solid var(--border-subtle)", display: "flex", alignItems: "center", gap: 12 } },
        hS3("div", { style: { flex: 1 } },
          hS3("div", { style: { fontSize: 13, fontWeight: 500 } }, "Run " + activeRun),
          hS3("div", { className: "t-mono", style: { fontSize: 11, color: "var(--fg-muted)" } }, "trigger: " + Data.workflowRuns.find(r=>r.id===activeRun).trigger)
        ),
        hS3("div", { style: { display: "flex", gap: 2, background: "var(--bg-sunk)", borderRadius: 7, padding: 2 } },
          [{ id: "runs", label: "Run" }, { id: "builder", label: "Builder" }].map(t => hS3("button", {
            key: t.id, onClick: () => setMode(t.id),
            style: {
              padding: "4px 10px", borderRadius: 5, fontSize: 11.5, fontWeight: 500,
              background: mode === t.id ? "var(--bg-raised)" : "transparent",
              color: mode === t.id ? "var(--fg-primary)" : "var(--fg-tertiary)",
              boxShadow: mode === t.id ? "var(--shadow-sm)" : "none"
            }
          }, t.label))
        )
      ),
      mode === "runs" ? hS3(RunInspector, { run: Data.workflowRuns.find(r => r.id === activeRun) }) : hS3(WorkflowBuilder)
    )
  );
}

function RunInspector({ run }) {
  const nodes = [
    { id: "n1", name: "trigger.tag_created", status: "completed", elapsed: "8ms" },
    { id: "n2", name: "github.fetch_commits_since", status: "completed", elapsed: "420ms" },
    { id: "n3", name: "llm.summarize_commits", status: "completed", elapsed: "2.4s" },
    { id: "n4", name: "llm.categorize", status: run.status === "running" ? "running" : "completed", elapsed: run.status === "running" ? "…" : "1.1s" },
    { id: "n5", name: "notion.create_page", status: run.status === "running" ? "pending" : run.status === "failed" ? "failed" : "completed", elapsed: run.status === "running" ? "—" : run.status === "failed" ? "12s" : "320ms" },
  ];
  return hS3("div", { style: { flex: 1, display: "flex", minHeight: 0 } },
    hS3("div", { style: { flex: 1, overflowY: "auto", padding: 20, background: "var(--bg-canvas)" } },
      hS3("div", { style: { maxWidth: 780, margin: "0 auto" } },
        hS3("div", { style: { display: "flex", alignItems: "center", gap: 12, marginBottom: 16 } },
          hS3("span", {
            className: "chip",
            style: {
              background: run.status === "running" ? "var(--status-info-soft)" : run.status === "completed" ? "var(--status-live-soft)" : "var(--status-error-soft)",
              color: run.status === "running" ? "var(--status-info)" : run.status === "completed" ? "var(--status-live)" : "var(--status-error)",
              height: 22
            }
          },
            hS3("span", { className: "chip-dot" }), run.status
          ),
          hS3("span", { className: "t-mono", style: { fontSize: 11.5, color: "var(--fg-tertiary)" } }, run.trigger),
          hS3("span", { style: { flex: 1 } }),
          hS3("button", { className: "btn btn-outline btn-sm" }, hS3(Icons.Refresh, { size: 12 }), "Retry"),
          run.status === "running" && hS3("button", { className: "btn btn-outline btn-sm" }, hS3(Icons.Stop, { size: 12 }), "Stop")
        ),
        hS3("div", { className: "card", style: { padding: 0, overflow: "hidden" } },
          nodes.map((n, i) => {
            const dot = n.status === "completed" ? "var(--status-live)" :
                        n.status === "running" ? "var(--status-info)" :
                        n.status === "failed" ? "var(--status-error)" : "var(--fg-muted)";
            return hS3("div", {
              key: n.id,
              style: { padding: "10px 14px", display: "flex", gap: 12, alignItems: "center",
                       borderBottom: i < nodes.length - 1 ? "1px solid var(--border-subtle)" : "none" }
            },
              hS3("span", { className: "dot", style: { background: dot } }),
              hS3("span", { className: "t-mono", style: { fontSize: 12, color: "var(--fg-muted)", width: 16 } }, (i+1).toString().padStart(2, "0")),
              hS3("span", { className: "t-mono", style: { fontSize: 12, flex: 1 } }, n.name),
              hS3("span", { style: { fontSize: 11.5, color: "var(--fg-tertiary)", textTransform: "capitalize" } }, n.status),
              hS3("span", { className: "t-mono", style: { fontSize: 11.5, color: "var(--fg-muted)", width: 60, textAlign: "right" } }, n.elapsed)
            );
          })
        ),
      )
    ),
    hS3("div", { style: { width: 340, borderLeft: "1px solid var(--border-subtle)", background: "var(--bg-surface)", padding: 16, overflowY: "auto" } },
      hS3("div", { className: "t-h3", style: { marginBottom: 10 } }, "Node inspector"),
      hS3("div", { className: "t-over", style: { marginBottom: 4 } }, "Output"),
      hS3("pre", {
        className: "mono card",
        style: { padding: 10, fontSize: 11.5, margin: 0, whiteSpace: "pre-wrap", background: "var(--bg-sunk)", color: "var(--fg-secondary)" }
      },
`{
  "summary": "v4.2: auth refactor, 22 fixes",
  "categories": {
    "Breaking":  3,
    "Features": 11,
    "Fixes":    22
  }
}`)
    )
  );
}

function WorkflowBuilder() {
  const [selectedNode, setSelectedNode] = useStateS3("n4");
  const nodes = {
    n1: { label: "tag_created", tone: "blue", kind: "trigger" },
    n2: { label: "fetch_commits", tone: "teal", kind: "io" },
    n3: { label: "summarize", tone: "purple", kind: "ai" },
    n4: { label: "categorize", tone: "purple", kind: "ai", running: true },
    n5: { label: "create_page", tone: "green", kind: "io" },
  };
  return hS3("div", { style: { flex: 1, display: "flex", flexDirection: "column", minHeight: 0 } },
    // Top row: palette + canvas + right inspector
    hS3("div", { style: { flex: 1, display: "flex", minHeight: 0 } },
      // Node palette
      hS3("div", { style: { width: 200, borderRight: "1px solid var(--border-subtle)", padding: 12, overflowY: "auto", background: "var(--bg-surface)" } },
        hS3("div", { className: "t-over", style: { marginBottom: 8 } }, "Nodes"),
        [
          { k: "Trigger", items: ["tag_created", "webhook", "cron"] },
          { k: "Flow", items: ["branch", "parallel", "loop"] },
          { k: "AI", items: ["llm_call", "agent_step", "embed"] },
          { k: "IO", items: ["http_request", "github.*", "notion.*"] },
        ].map(grp => hS3("div", { key: grp.k, style: { marginBottom: 10 } },
          hS3("div", { style: { fontSize: 11, color: "var(--fg-tertiary)", fontWeight: 600, marginBottom: 4 } }, grp.k),
          grp.items.map(it => hS3("div", {
            key: it,
            style: {
              padding: "6px 8px", borderRadius: 6, fontSize: 12, background: "var(--bg-sunk)",
              border: "1px solid var(--border-subtle)", marginBottom: 3, cursor: "grab",
              fontFamily: "JetBrains Mono, monospace"
            }
          }, it))
        ))
      ),
      // Canvas
      hS3("div", { style: {
        flex: 1, position: "relative", overflow: "hidden",
        background: "var(--bg-canvas)",
        backgroundImage: "radial-gradient(circle, var(--border-default) 1px, transparent 1px)",
        backgroundSize: "20px 20px"
      } },
        Object.entries(nodes).map(([id, n], i) => {
          const positions = { n1: [40,50], n2: [260,50], n3: [500,50], n4: [260,180], n5: [500,180] };
          const [x, y] = positions[id];
          return hS3("div", { key: id, onClick: () => setSelectedNode(id), style: { position: "absolute", left: x, top: y, cursor: "pointer" } },
            hS3(SelectableNode, { ...n, selected: selectedNode === id })
          );
        }),
        hS3(Connectors),
        hS3("div", { style: { position: "absolute", bottom: 12, left: 12, display: "flex", gap: 6 } },
          ["100%", "Fit", "Lock"].map(l => hS3("button", { key: l, className: "btn btn-outline btn-sm", style: { background: "var(--bg-raised)" } }, l))
        )
      ),
      // Right: Node Inspector + live run stream
      hS3("div", { style: { width: 340, borderLeft: "1px solid var(--border-subtle)", background: "var(--bg-surface)", display: "flex", flexDirection: "column", minHeight: 0 } },
        hS3("div", { style: { padding: "10px 14px", borderBottom: "1px solid var(--border-subtle)", display: "flex", alignItems: "center", gap: 8 } },
          hS3("div", { className: `avatar tone-${nodes[selectedNode].tone}`, style: { width: 22, height: 22, borderRadius: 5 } },
            hS3(nodes[selectedNode].kind === "ai" ? Icons.Sparkle : nodes[selectedNode].kind === "trigger" ? Icons.Zap : Icons.Code, { size: 12 })
          ),
          hS3("div", { style: { flex: 1, minWidth: 0 } },
            hS3("div", { style: { fontSize: 12.5, fontWeight: 600 } }, nodes[selectedNode].label),
            hS3("div", { style: { fontSize: 10.5, color: "var(--fg-muted)", textTransform: "uppercase", letterSpacing: "0.06em" } }, nodes[selectedNode].kind)
          ),
          nodes[selectedNode].running && hS3("span", { className: "chip", style: { height: 19, background: "var(--status-info-soft)", color: "var(--status-info)" } },
            hS3("span", { className: "chip-dot" }), "running"
          )
        ),
        hS3("div", { style: { flex: 1, overflowY: "auto", padding: 12, display: "flex", flexDirection: "column", gap: 10 } },
          // Config
          hS3("div", null,
            hS3("div", { className: "t-over", style: { marginBottom: 6 } }, "Config"),
            hS3("div", { className: "card", style: { padding: 10 } },
              [["model","claude-sonnet-4.5"],["temperature","0.3"],["max_tokens","1024"]].map(r =>
                hS3("div", { key: r[0], style: { display: "flex", justifyContent: "space-between", padding: "4px 0", fontSize: 11.5 } },
                  hS3("span", { style: { color: "var(--fg-tertiary)" } }, r[0]),
                  hS3("span", { className: "t-mono", style: { color: "var(--fg-primary)" } }, r[1])
                )
              )
            )
          ),
          // Live run stream (chat-like)
          hS3("div", null,
            hS3("div", { style: { display: "flex", alignItems: "center", gap: 6, marginBottom: 6 } },
              hS3("span", { className: "t-over" }, "Live run"),
              hS3("span", { className: "dot dot-live", style: { marginLeft: 2 } })
            ),
            hS3("div", { style: { display: "flex", flexDirection: "column", gap: 6 } },
              // thinking
              hS3("div", { className: "card", style: { padding: 9, fontSize: 11.5 } },
                hS3("div", { style: { display: "flex", alignItems: "center", gap: 5, marginBottom: 4, color: "var(--fg-tertiary)" } },
                  hS3(Icons.Sparkle, { size: 11 }),
                  hS3("span", { style: { fontSize: 10.5, fontWeight: 600, textTransform: "uppercase", letterSpacing: "0.05em" } }, "thinking")
                ),
                hS3("div", { style: { color: "var(--fg-secondary)", fontStyle: "italic" } }, "커밋 메시지의 prefix 패턴으로 분류하고, 본문에서 BREAKING·feat·fix 키워드를 가중치로…")
              ),
              // tool use
              hS3("div", { className: "card", style: { padding: 9, fontSize: 11.5 } },
                hS3("div", { style: { display: "flex", alignItems: "center", gap: 5, marginBottom: 4, color: "var(--fg-tertiary)" } },
                  hS3(Icons.Tool, { size: 11 }),
                  hS3("span", { style: { fontSize: 10.5, fontWeight: 600, textTransform: "uppercase", letterSpacing: "0.05em" } }, "tool · regex_match"),
                  hS3("span", { style: { fontSize: 10, color: "var(--status-live)", marginLeft: "auto" } }, "180ms")
                ),
                hS3("pre", { className: "mono", style: { margin: 0, fontSize: 11, background: "var(--bg-sunk)", padding: 6, borderRadius: 5, whiteSpace: "pre-wrap" } },
                  `{ "pattern": "^(feat|fix|BREAKING)", "input": 36 }`
                )
              ),
              // text response (streaming)
              hS3("div", { className: "card", style: { padding: 9, fontSize: 11.5 } },
                hS3("div", { style: { display: "flex", alignItems: "center", gap: 5, marginBottom: 4, color: "var(--fg-tertiary)" } },
                  hS3(Icons.Message, { size: 11 }),
                  hS3("span", { style: { fontSize: 10.5, fontWeight: 600, textTransform: "uppercase", letterSpacing: "0.05em" } }, "response")
                ),
                hS3("div", { style: { color: "var(--fg-primary)" } },
                  "3 Breaking, 11 Features, 22 Fixes로 분류했습니다",
                  hS3("span", { style: { display: "inline-block", width: 2, height: 12, background: "var(--accent-blue)", marginLeft: 2, verticalAlign: "middle", animation: "blink 1s step-end infinite" } })
                )
              )
            )
          ),
          // Past input/output
          hS3("div", null,
            hS3("div", { className: "t-over", style: { marginBottom: 6 } }, "Input"),
            hS3("pre", { className: "mono card", style: { padding: 8, margin: 0, fontSize: 11, whiteSpace: "pre-wrap", background: "var(--bg-sunk)" } },
              `{ "commits": 36, "since": "v4.1.3" }`
            )
          )
        )
      )
    ),
    // Bottom panel: AI Assistant
    hS3("div", { style: { borderTop: "1px solid var(--border-subtle)", background: "var(--bg-surface)", display: "flex", flexDirection: "column", maxHeight: 220 } },
      hS3("div", { style: { padding: "8px 14px", display: "flex", alignItems: "center", gap: 8, borderBottom: "1px solid var(--border-subtle)" } },
        hS3(Icons.Sparkle, { size: 13, style: { color: "var(--accent-blue)" } }),
        hS3("span", { className: "t-h3" }, "AI Assistant"),
        hS3("span", { style: { flex: 1 } }),
        hS3("button", { className: "btn btn-ghost btn-sm btn-icon" }, hS3(Icons.ChevronDown, { size: 13 }))
      ),
      hS3("div", { style: { flex: 1, overflowY: "auto", padding: "10px 14px", display: "flex", gap: 10 } },
        hS3("div", { className: "card", style: { padding: 9, fontSize: 12, color: "var(--fg-secondary)", flex: 1 } },
          "`categorize` 노드를 병렬로 돌리면 전체 실행 시간이 1.6초 → 0.8초로 줄어듭니다. ",
          hS3("button", { style: { color: "var(--accent-blue)", fontWeight: 500, fontSize: 12 } }, "적용 →")
        ),
        hS3("div", { className: "card", style: { padding: 9, fontSize: 12, color: "var(--fg-secondary)", flex: 1 } },
          "오류 핸들러가 `create_page` 뒤에 없습니다. 실패 시 Slack 알림을 추가할까요?"
        )
      ),
      hS3("div", { style: { padding: "8px 14px", borderTop: "1px solid var(--border-subtle)" } },
        hS3("div", { style: { display: "flex", gap: 6, padding: 8, border: "1px solid var(--border-default)", borderRadius: 8, background: "var(--bg-raised)" } },
          hS3("input", { placeholder: "Ask to edit workflow…", style: { flex: 1, background: "transparent", border: "none", outline: "none", fontSize: 12.5 } }),
          hS3("button", { className: "btn btn-primary btn-sm btn-icon" }, hS3(Icons.ArrowUp, { size: 12 }))
        )
      )
    )
  );
}

function SelectableNode({ label, tone, kind, running, selected }) {
  return hS3("div", {
    style: {
      width: 180,
      background: "var(--bg-raised)",
      border: "1px solid " + (selected ? "var(--accent-blue)" : "var(--border-default)"),
      boxShadow: selected ? "0 0 0 3px var(--accent-blue-soft), var(--shadow-md)" : "var(--shadow-md)",
      borderRadius: 8, padding: 10, zIndex: 2
    }
  },
    hS3("div", { style: { display: "flex", alignItems: "center", gap: 7, marginBottom: 6 } },
      hS3("div", { className: `avatar tone-${tone}`, style: { width: 20, height: 20, fontSize: 9, borderRadius: 5 } },
        hS3(kind === "ai" ? Icons.Sparkle : kind === "trigger" ? Icons.Zap : Icons.Code, { size: 11 })
      ),
      hS3("span", { style: { fontSize: 10, color: "var(--fg-muted)", textTransform: "uppercase", letterSpacing: "0.06em", fontWeight: 600 } }, kind),
      running && hS3("span", { className: "dot dot-live", style: { marginLeft: "auto" } })
    ),
    hS3("div", { className: "t-mono", style: { fontSize: 12 } }, label)
  );
}

function Connectors() {
  return hS3("svg", {
    style: { position: "absolute", inset: 0, width: "100%", height: "100%", pointerEvents: "none" }
  },
    hS3("defs", null,
      hS3("marker", { id: "arrow", viewBox: "0 0 10 10", refX: 8, refY: 5, markerWidth: 7, markerHeight: 7, orient: "auto" },
        hS3("path", { d: "M 0 0 L 10 5 L 0 10 z", fill: "var(--fg-muted)" })
      )
    ),
    [
      { x1: 220, y1: 80, x2: 260, y2: 80 },
      { x1: 440, y1: 80, x2: 500, y2: 80 },
      { x1: 590, y1: 110, x2: 350, y2: 200 },
      { x1: 440, y1: 210, x2: 500, y2: 210 },
    ].map((l, i) => hS3("path", {
      key: i,
      d: `M${l.x1} ${l.y1} C${l.x1+30} ${l.y1}, ${l.x2-30} ${l.y2}, ${l.x2} ${l.y2}`,
      stroke: "var(--border-strong)", strokeWidth: 1.5, fill: "none", markerEnd: "url(#arrow)"
    }))
  );
}

window.Screens3 = { WorkflowsScreen, WorkflowDetailScreen };
