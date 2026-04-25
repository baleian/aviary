/* global React, Icons, Shell, Data */
const { useState: useStateS1, useMemo: useMemoS1 } = React;
const hS1 = React.createElement;
const { Spark, KindBadge } = Shell;

/* ============================================================
 * DASHBOARD
 * ============================================================ */
function DashboardScreen({ onRoute }) {
  const stats = Data.stats;
  const publishedAgents = Data.agents.filter(a => a.kind === "published");
  const publishedWorkflows = Data.workflows.filter(w => w.kind === "published");
  const totalAgentInstalls = publishedAgents.reduce((s, a) => s + (a.installs || 0), 0);
  const totalWfInstalls = publishedWorkflows.reduce((s, w) => s + (w.installs || 0), 0);
  const statCards = [
    { key: "Chat sessions",      val: stats.sessions.value,     delta: stats.sessions.delta,     spark: stats.sessions.spark,     color: "var(--accent-blue)", sub: "this week" },
    { key: "Workflow runs",      val: stats.workflowRuns.value, delta: stats.workflowRuns.delta, spark: stats.workflowRuns.spark, color: "var(--status-live)", sub: "this week" },
    { key: "Published agents",   val: publishedAgents.length,   delta: totalAgentInstalls + " imports",    spark: null, color: "var(--accent-blue)", sub: "by others" },
    { key: "Published workflows",val: publishedWorkflows.length,delta: (totalWfInstalls || 0) + " imports", spark: null, color: "var(--status-live)", sub: "by others" },
  ];

  return hS1("div", {
    style: { overflowY: "auto", height: "100%", padding: "24px 32px 48px" }
  },
    // Hero
    hS1("div", { style: { maxWidth: 1400, margin: "0 auto" } },
      hS1("div", { style: { display: "flex", alignItems: "baseline", justifyContent: "space-between", marginBottom: 20 } },
        hS1("div", null,
          hS1("div", { className: "t-hero" }, "안녕하세요, 민수님"),
          hS1("div", { className: "t-small fg-tertiary", style: { marginTop: 2 } }, "이번 주 활동 요약 · 목요일 · 4월 25일")
        ),
        hS1("div", { style: { display: "flex", gap: 8 } },
          hS1("button", { className: "btn btn-outline btn-sm" },
            hS1(Icons.Plus, { size: 13 }), "New Workflow"
          ),
          hS1("button", { className: "btn btn-primary btn-sm" },
            hS1(Icons.Plus, { size: 13 }), "New Agent"
          )
        )
      ),

      // Stat cards
      hS1("div", { style: { display: "grid", gridTemplateColumns: "repeat(4, 1fr)", gap: 12, marginBottom: 24 } },
        statCards.map(c => hS1("div", { key: c.key, className: "card", style: { padding: 14 } },
          hS1("div", { style: { display: "flex", alignItems: "flex-start", justifyContent: "space-between", marginBottom: c.spark ? 10 : 0 } },
            hS1("div", null,
              hS1("div", { className: "t-small fg-tertiary" }, c.key),
              hS1("div", { style: { fontSize: 28, fontWeight: 600, letterSpacing: "-0.015em", marginTop: 2, fontVariantNumeric: "tabular-nums" } }, c.val)
            ),
            hS1("div", { style: { textAlign: "right" } },
              hS1("div", { style: { fontSize: 11.5, color: "var(--status-live)", fontWeight: 600, fontVariantNumeric: "tabular-nums" } }, c.delta),
              hS1("div", { style: { fontSize: 10.5, color: "var(--fg-muted)", marginTop: 2 } }, c.sub)
            )
          ),
          c.spark && hS1(Spark, { data: c.spark, color: c.color, width: 220, height: 32 })
        ))
      ),
      // Two column layout
      hS1("div", { style: { display: "grid", gridTemplateColumns: "1.3fr 1fr", gap: 16 } },
        // Continue working — sessions + runs
        hS1("div", { className: "card", style: { padding: 0 } },
          hS1("div", { style: { padding: "12px 16px", display: "flex", alignItems: "center", justifyContent: "space-between", borderBottom: "1px solid var(--border-subtle)" } },
            hS1("div", { className: "t-h3" }, "Recent chat sessions"),
            hS1("button", {
              onClick: () => onRoute({ primary: "agents" }),
              style: { fontSize: 11.5, color: "var(--accent-blue)", fontWeight: 500 }
            }, "All agents →")
          ),
          hS1("div", null,
            Data.recentSessions.map((s, i) => hS1("button", {
              key: s.id,
              onClick: () => onRoute({ primary: "agent", agentId: Data.agents.find(a => a.name === s.agent)?.id, sessionId: s.id }),
              style: {
                display: "flex", gap: 12, padding: "12px 16px", width: "100%", textAlign: "left",
                borderBottom: i < Data.recentSessions.length - 1 ? "1px solid var(--border-subtle)" : "none",
                transition: "background 120ms",
              },
              onMouseOver: e => e.currentTarget.style.background = "var(--bg-hover)",
              onMouseOut: e => e.currentTarget.style.background = "transparent"
            },
              hS1("div", { className: `avatar tone-${s.tone}`, style: { width: 28, height: 28, fontSize: 11 } },
                hS1(Icons.Message, { size: 14 })
              ),
              hS1("div", { style: { flex: 1, minWidth: 0 } },
                hS1("div", { style: { display: "flex", alignItems: "center", gap: 8, marginBottom: 2 } },
                  hS1("span", { style: { fontSize: 13, fontWeight: 500 } }, s.title),
                  hS1("span", { style: { fontSize: 11, color: "var(--fg-muted)" } }, "·"),
                  hS1("span", { style: { fontSize: 11.5, color: "var(--fg-tertiary)" } }, s.agent)
                ),
                hS1("div", { className: "truncate", style: { fontSize: 12, color: "var(--fg-tertiary)" } }, s.preview)
              ),
              hS1("div", { style: { textAlign: "right", flexShrink: 0 } },
                hS1("div", { style: { fontSize: 11, color: "var(--fg-muted)" } }, s.when),
                hS1("div", { style: { fontSize: 11, color: "var(--fg-muted)", marginTop: 2 } }, s.msgs + " msg")
              )
            ))
          )
        ),

          hS1("div", { style: { display: "flex", flexDirection: "column", gap: 16 } },
          // Published reach — expanded
          hS1("div", { className: "card", style: { padding: 16 } },
            hS1("div", { style: { display: "flex", justifyContent: "space-between", marginBottom: 10 } },
              hS1("div", { className: "t-h3" }, "Published reach"),
              hS1("button", {
                onClick: () => onRoute({ primary: "marketplace", filter: "mine" }),
                style: { fontSize: 11.5, color: "var(--accent-blue)", fontWeight: 500 }
              }, "View all →")
            ),
            hS1("div", { className: "t-over", style: { marginBottom: 6 } }, "Agents"),
            Data.agents.filter(a => a.kind === "published").map(a => hS1("div", {
              key: a.id,
              style: { display: "flex", alignItems: "center", gap: 10, padding: "8px 0", borderTop: "1px solid var(--border-subtle)" }
            },
              hS1("div", { className: `avatar tone-${a.tone}`, style: { width: 26, height: 26, fontSize: 10.5 } }, a.icon),
              hS1("div", { style: { flex: 1, minWidth: 0 } },
                hS1("div", { style: { fontSize: 12.5, fontWeight: 500 } }, a.name),
                hS1("div", { style: { fontSize: 11, color: "var(--fg-tertiary)" } }, a.version)
              ),
              hS1("div", { style: { textAlign: "right" } },
                hS1("div", { style: { fontSize: 12.5, fontWeight: 600, fontVariantNumeric: "tabular-nums" } }, a.installs),
                hS1("div", { style: { fontSize: 10.5, color: "var(--fg-muted)" } }, "installs")
              )
            )),
            hS1("div", { className: "t-over", style: { marginTop: 14, marginBottom: 6 } }, "Workflows"),
            Data.workflows.filter(w => w.kind === "published").map(w => hS1("div", {
              key: w.id,
              style: { display: "flex", alignItems: "center", gap: 10, padding: "8px 0", borderTop: "1px solid var(--border-subtle)" }
            },
              hS1("div", { className: `avatar tone-${w.tone}`, style: { width: 26, height: 26, fontSize: 10.5 } },
                hS1(Icons.Workflows, { size: 13 })
              ),
              hS1("div", { style: { flex: 1, minWidth: 0 } },
                hS1("div", { style: { fontSize: 12.5, fontWeight: 500 } }, w.name),
                hS1("div", { style: { fontSize: 11, color: "var(--fg-tertiary)" } }, w.version)
              ),
              hS1("div", { style: { textAlign: "right" } },
                hS1("div", { style: { fontSize: 12.5, fontWeight: 600, fontVariantNumeric: "tabular-nums" } }, w.installs || 0),
                hS1("div", { style: { fontSize: 10.5, color: "var(--fg-muted)" } }, "installs")
              )
            ))
          )
        ),
      ),

      // Recent runs wide
      hS1("div", { className: "card", style: { marginTop: 16, padding: 0 } },
        hS1("div", { style: { padding: "12px 16px", display: "flex", alignItems: "center", justifyContent: "space-between", borderBottom: "1px solid var(--border-subtle)" } },
          hS1("div", { className: "t-h3" }, "Recent workflow runs"),
          hS1("button", {
            onClick: () => onRoute({ primary: "workflows" }),
            style: { fontSize: 11.5, color: "var(--accent-blue)", fontWeight: 500 }
          }, "All workflows →")
        ),
        hS1("div", { style: { padding: "0 16px" } },
          hS1("div", {
            style: {
              display: "grid", gridTemplateColumns: "1.2fr 2fr 120px 100px 100px 28px", gap: 12,
              padding: "8px 0", borderBottom: "1px solid var(--border-subtle)",
              fontSize: 10.5, color: "var(--fg-muted)", fontWeight: 600, letterSpacing: "0.06em", textTransform: "uppercase"
            }
          },
            hS1("div", null, "Workflow"), hS1("div", null, "Trigger"),
            hS1("div", null, "Status"), hS1("div", null, "Duration"), hS1("div", null, "When"), hS1("div", null, "")
          ),
          Data.recentRuns.map(r => hS1("div", {
            key: r.id,
            style: {
              display: "grid", gridTemplateColumns: "1.2fr 2fr 120px 100px 100px 28px", gap: 12,
              padding: "10px 0", alignItems: "center",
              borderBottom: "1px solid var(--border-subtle)"
            }
          },
            hS1("div", { style: { display: "flex", gap: 8, alignItems: "center" } },
              hS1("div", { className: `avatar tone-${r.tone}`, style: { width: 22, height: 22, fontSize: 9.5 } },
                hS1(Icons.Workflows, { size: 12 })
              ),
              hS1("span", { style: { fontSize: 13, fontWeight: 500 } }, r.workflow)
            ),
            hS1("span", { className: "t-mono truncate", style: { color: "var(--fg-tertiary)" } }, r.trigger),
            hS1("span", null,
              hS1("span", {
                className: "chip",
                style: {
                  background: r.status === "completed" ? "var(--status-live-soft)" :
                              r.status === "running"   ? "var(--status-info-soft)" :
                              r.status === "failed"    ? "var(--status-error-soft)" : "var(--bg-hover)",
                  color: r.status === "completed" ? "var(--status-live)" :
                         r.status === "running"   ? "var(--status-info)" :
                         r.status === "failed"    ? "var(--status-error)" : "var(--fg-tertiary)"
                }
              },
                hS1("span", { className: "chip-dot", style: {
                  background: r.status === "completed" ? "var(--status-live)" :
                              r.status === "running"   ? "var(--status-info)" :
                              r.status === "failed"    ? "var(--status-error)" : "var(--fg-muted)"
                } }),
                r.status
              )
            ),
            hS1("span", { className: "t-mono", style: { color: "var(--fg-secondary)" } }, r.duration),
            hS1("span", { style: { fontSize: 12, color: "var(--fg-tertiary)" } }, r.when),
            hS1("button", { className: "btn btn-ghost btn-icon btn-sm" }, hS1(Icons.ChevronRight, { size: 13 }))
          ))
        )
      )
    )
  );
}

/* ============================================================
 * AGENTS MAIN — grid of agent cards
 * ============================================================ */
function AgentsScreen({ onRoute }) {
  const [filter, setFilter] = useStateS1("all");
  const [sort, setSort] = useStateS1("recent");
  const [view, setView] = useStateS1("grid"); // grid | list

  const filtered = useMemoS1(() => {
    let a = Data.agents.slice();
    if (filter !== "all") a = a.filter(x => x.kind === filter);
    return a;
  }, [filter]);

  const counts = {
    all: Data.agents.length,
    private: Data.agents.filter(a => a.kind === "private").length,
    published: Data.agents.filter(a => a.kind === "published").length,
    imported: Data.agents.filter(a => a.kind === "imported").length,
  };

  return hS1("div", { style: { height: "100%", overflowY: "auto" } },
    // Sub-header with filters
    hS1("div", { style: { padding: "20px 32px 0" } },
      hS1("div", { style: { maxWidth: 1400, margin: "0 auto" } },
        hS1("div", { style: { display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 16 } },
          hS1("div", null,
            hS1("div", { className: "t-hero" }, "Agents"),
            hS1("div", { className: "t-small fg-tertiary", style: { marginTop: 2 } }, "내가 만든 에이전트와 Marketplace에서 가져온 에이전트를 관리합니다.")
          ),
          hS1("div", { style: { display: "flex", gap: 8 } },
            hS1("button", { className: "btn btn-outline btn-sm" },
              hS1(Icons.Upload, { size: 13 }), "Import"
            ),
            hS1("button", { className: "btn btn-primary btn-sm" },
              hS1(Icons.Plus, { size: 13 }), "New Agent"
            )
          )
        ),

        // Filter tabs + controls
        hS1("div", {
          style: {
            display: "flex", alignItems: "center", gap: 12,
            paddingBottom: 12, borderBottom: "1px solid var(--border-subtle)"
          }
        },
          hS1("div", { style: { display: "flex", gap: 2, padding: 2, background: "var(--bg-sunk)", borderRadius: 8 } },
            [
              { id: "all", label: "All" },
              { id: "private", label: "Private", icon: Icons.Lock },
              { id: "published", label: "Published", icon: Icons.Globe },
              { id: "imported", label: "Imported", icon: Icons.Download },
            ].map(tab => {
              const active = filter === tab.id;
              return hS1("button", {
                key: tab.id,
                onClick: () => setFilter(tab.id),
                style: {
                  display: "flex", alignItems: "center", gap: 6,
                  padding: "6px 12px", borderRadius: 6,
                  fontSize: 12.5, fontWeight: 500,
                  background: active ? "var(--bg-raised)" : "transparent",
                  color: active ? "var(--fg-primary)" : "var(--fg-tertiary)",
                  boxShadow: active ? "var(--shadow-sm)" : "none",
                }
              },
                tab.icon && hS1(tab.icon, { size: 12 }),
                tab.label,
                hS1("span", {
                  style: {
                    fontSize: 11, color: "var(--fg-muted)",
                    fontVariantNumeric: "tabular-nums", marginLeft: 2
                  }
                }, counts[tab.id])
              );
            })
          ),
          hS1("div", { style: { flex: 1 } }),
          hS1("div", { style: { position: "relative" } },
            hS1("input", {
              className: "input",
              placeholder: "Filter agents…",
              style: { width: 220, paddingLeft: 30 }
            }),
            hS1("div", { style: { position: "absolute", left: 9, top: 8, color: "var(--fg-muted)" } },
              hS1(Icons.Search, { size: 14 })
            )
          ),
          hS1("button", { className: "btn btn-outline btn-sm" },
            hS1(Icons.Filter, { size: 13 }), "Filter"
          ),
          hS1("div", { style: { display: "flex", background: "var(--bg-sunk)", borderRadius: 6, padding: 1 } },
            [
              { id: "grid", icon: Icons.Dashboard },
              { id: "list", icon: Icons.Layers },
            ].map(v => hS1("button", {
              key: v.id,
              onClick: () => setView(v.id),
              style: {
                width: 24, height: 24, display: "grid", placeItems: "center",
                borderRadius: 5,
                background: view === v.id ? "var(--bg-raised)" : "transparent",
                color: view === v.id ? "var(--fg-primary)" : "var(--fg-muted)"
              }
            }, hS1(v.icon, { size: 13 })))
          )
        )
      )
    ),

    // Grid
    hS1("div", { style: { padding: "20px 32px 40px" } },
      hS1("div", { style: { maxWidth: 1400, margin: "0 auto" } },
        view === "grid"
          ? hS1("div", { style: { display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(288px, 1fr))", gap: 12 } },
              filtered.map(a => hS1(AgentCard, { key: a.id, agent: a, onClick: () => onRoute({ primary: "agent", agentId: a.id }) }))
            )
          : hS1(AgentList, { agents: filtered, onClick: a => onRoute({ primary: "agent", agentId: a.id }) })
      )
    )
  );
}

function AgentCard({ agent, onClick }) {
  return hS1("button", {
    onClick,
    className: "card card-hover",
    style: {
      padding: 14, textAlign: "left", display: "flex", flexDirection: "column", gap: 10,
      cursor: "pointer", position: "relative",
    },
    onMouseOver: e => { e.currentTarget.style.transform = "translateY(-1px)"; e.currentTarget.style.boxShadow = "var(--shadow-md)"; },
    onMouseOut:  e => { e.currentTarget.style.transform = "translateY(0)"; e.currentTarget.style.boxShadow = "none"; }
  },
    hS1("div", { style: { display: "flex", alignItems: "flex-start", gap: 10 } },
      hS1("div", { className: `avatar tone-${agent.tone}`, style: { width: 36, height: 36, fontSize: 13 } }, agent.icon),
      hS1("div", { style: { flex: 1, minWidth: 0 } },
        hS1("div", { style: { display: "flex", alignItems: "center", gap: 6, marginBottom: 1 } },
          hS1("span", { style: { fontSize: 14, fontWeight: 600, letterSpacing: "-0.005em" } }, agent.name),
          agent.hasUpdate && hS1("span", {
            title: "Update available",
            style: { width: 6, height: 6, borderRadius: 99, background: "var(--status-warn)" }
          })
        ),
        hS1("div", { style: { display: "flex", alignItems: "center", gap: 6, fontSize: 11.5, color: "var(--fg-tertiary)" } },
          hS1(KindBadge, { kind: agent.kind }),
          agent.version && hS1("span", { className: "t-mono", style: { fontSize: 11 } }, agent.version),
          agent.author && hS1("span", null, "·", " ", agent.author)
        )
      ),
      hS1("button", {
        className: "btn btn-ghost btn-icon btn-sm",
        onClick: e => e.stopPropagation(),
        style: { opacity: 0.6 }
      }, hS1(Icons.MoreV, { size: 14 }))
    ),
    hS1("div", {
      style: { fontSize: 12.5, color: "var(--fg-secondary)", lineHeight: 1.45, minHeight: 36,
               display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden" }
    }, agent.desc),
    hS1("div", { style: { height: 1, background: "var(--border-subtle)", margin: "2px 0" } }),
    hS1("div", { style: { display: "flex", alignItems: "center", gap: 12, fontSize: 11.5, color: "var(--fg-tertiary)" } },
      hS1("span", { style: { display: "flex", alignItems: "center", gap: 4 } },
        hS1(Icons.Tool, { size: 12 }),
        hS1("span", { className: "t-mono", style: { fontVariantNumeric: "tabular-nums" } }, agent.tools),
        hS1("span", { style: { color: "var(--fg-muted)" } }, "tools")
      ),
      hS1("span", { style: { display: "flex", alignItems: "center", gap: 4 } },
        hS1(Icons.Message, { size: 12 }),
        hS1("span", { className: "t-mono", style: { fontVariantNumeric: "tabular-nums" } }, agent.sessions),
        hS1("span", { style: { color: "var(--fg-muted)" } }, "sessions")
      ),
      hS1("span", { style: { flex: 1 } }),
      hS1("span", { style: { color: "var(--fg-muted)", fontSize: 11 } }, agent.lastUsed)
    )
  );
}

function AgentList({ agents, onClick }) {
  return hS1("div", { className: "card", style: { padding: 0, overflow: "hidden" } },
    hS1("div", {
      style: {
        display: "grid", gridTemplateColumns: "2fr 2.2fr 110px 80px 80px 110px 28px",
        gap: 12, padding: "10px 16px", borderBottom: "1px solid var(--border-subtle)",
        fontSize: 10.5, color: "var(--fg-muted)", fontWeight: 600, letterSpacing: "0.06em", textTransform: "uppercase"
      }
    },
      hS1("div", null, "Agent"), hS1("div", null, "Description"),
      hS1("div", null, "Kind"), hS1("div", null, "Tools"), hS1("div", null, "Sessions"),
      hS1("div", null, "Updated"), hS1("div", null, "")
    ),
    agents.map((a, i) => hS1("button", {
      key: a.id,
      onClick: () => onClick(a),
      style: {
        width: "100%", textAlign: "left",
        display: "grid", gridTemplateColumns: "2fr 2.2fr 110px 80px 80px 110px 28px",
        gap: 12, padding: "10px 16px", alignItems: "center",
        borderBottom: i < agents.length - 1 ? "1px solid var(--border-subtle)" : "none"
      },
      onMouseOver: e => e.currentTarget.style.background = "var(--bg-hover)",
      onMouseOut: e => e.currentTarget.style.background = "transparent"
    },
      hS1("div", { style: { display: "flex", gap: 10, alignItems: "center", minWidth: 0 } },
        hS1("div", { className: `avatar tone-${a.tone}`, style: { width: 26, height: 26, fontSize: 10.5 } }, a.icon),
        hS1("span", { className: "truncate", style: { fontSize: 13, fontWeight: 500 } }, a.name)
      ),
      hS1("span", { className: "truncate", style: { fontSize: 12, color: "var(--fg-tertiary)" } }, a.desc),
      hS1(KindBadge, { kind: a.kind }),
      hS1("span", { className: "t-mono", style: { fontVariantNumeric: "tabular-nums", color: "var(--fg-secondary)" } }, a.tools),
      hS1("span", { className: "t-mono", style: { fontVariantNumeric: "tabular-nums", color: "var(--fg-secondary)" } }, a.sessions),
      hS1("span", { style: { fontSize: 11.5, color: "var(--fg-muted)" } }, a.updated),
      hS1(Icons.ChevronRight, { size: 14, style: { color: "var(--fg-muted)" } })
    ))
  );
}

window.Screens1 = { DashboardScreen, AgentsScreen };
