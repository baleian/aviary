/* global React, Icons */
const { useState, useEffect, useRef } = React;
const h = React.createElement;

/* ============================================================
 * Shell: left rail + header + content slot
 * ============================================================ */
function Sidebar({ route, onRoute, collapsed, onToggleCollapse }) {
  const items = [
    { id: "dashboard", label: "Dashboard", icon: Icons.Dashboard },
    { id: "agents", label: "Agents", icon: Icons.Agents, count: 8 },
    { id: "workflows", label: "Workflows", icon: Icons.Workflows, count: 4 },
    { id: "marketplace", label: "Marketplace", icon: Icons.Marketplace },
  ];

  return h("aside", {
    style: {
      width: collapsed ? 56 : 220,
      background: "var(--bg-surface)",
      borderRight: "1px solid var(--border-subtle)",
      display: "flex", flexDirection: "column",
      transition: "width 180ms cubic-bezier(0.16, 1, 0.3, 1)",
      flexShrink: 0,
      position: "relative",
    }
  },
    // Brand
    h("div", {
      style: {
        height: 48, padding: collapsed ? "0 14px" : "0 14px",
        display: "flex", alignItems: "center", gap: 10,
        borderBottom: "1px solid var(--border-subtle)"
      }
    },
      h("div", {
        style: {
          width: 26, height: 26, borderRadius: 7,
          background: "var(--accent-blue)",
          display: "grid", placeItems: "center",
          color: "#fff", fontWeight: 700, fontSize: 13,
          letterSpacing: "-0.02em", flexShrink: 0,
        }
      }, "A"),
      !collapsed && h("div", { style: { fontWeight: 600, fontSize: 14, letterSpacing: "-0.01em" } }, "Aviary"),
      !collapsed && h("div", { style: { flex: 1 } }),
      !collapsed && h("div", { className: "chip", style: { height: 19, fontSize: 10.5, padding: "0 6px" } }, "Internal")
    ),

    // Nav items
    h("div", { style: { padding: "10px 8px", display: "flex", flexDirection: "column", gap: 2, flex: 1 } },
      items.map(it => {
        const active = route.primary === it.id;
        return h("button", {
          key: it.id,
          onClick: () => onRoute({ primary: it.id }),
          title: collapsed ? it.label : undefined,
          style: {
            display: "flex", alignItems: "center", gap: 10,
            padding: collapsed ? "7px 10px" : "7px 10px",
            height: 32,
            borderRadius: 7,
            color: active ? "var(--fg-primary)" : "var(--fg-secondary)",
            background: active ? "var(--bg-active)" : "transparent",
            fontSize: 13, fontWeight: active ? 500 : 450,
            position: "relative",
            justifyContent: collapsed ? "center" : "flex-start",
          },
          onMouseOver: (e) => { if (!active) e.currentTarget.style.background = "var(--bg-hover)"; },
          onMouseOut:  (e) => { if (!active) e.currentTarget.style.background = "transparent"; }
        },
          active && h("div", {
            style: {
              position: "absolute", left: -8, top: 8, bottom: 8, width: 2,
              background: "var(--accent-blue)", borderRadius: 2
            }
          }),
          h(it.icon, { size: 16 }),
          !collapsed && h("span", { style: { flex: 1, textAlign: "left" } }, it.label),
          !collapsed && it.count && h("span", {
            style: { fontSize: 11, color: "var(--fg-muted)", fontVariantNumeric: "tabular-nums" }
          }, it.count)
        );
      })
    ),

    // Collapse toggle
    h("div", { style: { padding: 8, borderTop: "1px solid var(--border-subtle)" } },
      h("button", {
        onClick: onToggleCollapse,
        title: collapsed ? "Expand" : "Collapse",
        style: {
          display: "flex", alignItems: "center", gap: 10,
          padding: "7px 10px", height: 30, width: "100%",
          borderRadius: 7, color: "var(--fg-tertiary)",
          justifyContent: collapsed ? "center" : "flex-start",
        },
        onMouseOver: (e) => e.currentTarget.style.background = "var(--bg-hover)",
        onMouseOut:  (e) => e.currentTarget.style.background = "transparent"
      },
        h(collapsed ? Icons.ChevronsRight : Icons.ChevronsLeft, { size: 15 }),
        !collapsed && h("span", { style: { fontSize: 12.5 } }, "Collapse")
      )
    )
  );
}

function Header({ onOpenSearch, onOpenUserMenu, onOpenNotifications, userMenuOpen, notifOpen, crumb, rightSlot, theme }) {
  return h("div", {
    style: {
      height: 48, borderBottom: "1px solid var(--border-subtle)",
      display: "flex", alignItems: "center", gap: 12,
      padding: "0 14px 0 18px",
      background: "var(--bg-canvas)",
      flexShrink: 0,
      position: "relative",
      zIndex: 3,
    }
  },
    // Breadcrumb / title slot
    h("div", { style: { display: "flex", alignItems: "center", gap: 8, minWidth: 0, flex: 1 } },
      crumb
    ),

    // Right cluster
    h("div", { style: { display: "flex", alignItems: "center", gap: 6 } },
      rightSlot,
      // Search pill
      h("button", {
        onClick: onOpenSearch,
        style: {
          height: 30, display: "flex", alignItems: "center", gap: 8,
          padding: "0 10px 0 10px",
          background: "var(--bg-sunk)", border: "1px solid var(--border-default)",
          borderRadius: 7, color: "var(--fg-muted)", minWidth: 200,
        }
      },
        h(Icons.Search, { size: 14 }),
        h("span", { style: { fontSize: 12.5, flex: 1, textAlign: "left" } }, "Search…"),
        h("span", { className: "kbd" }, "⌘"),
        h("span", { className: "kbd" }, "K")
      ),
      // Notifications
      h("button", {
        className: "btn btn-ghost btn-icon",
        onClick: onOpenNotifications,
        style: { position: "relative", background: notifOpen ? "var(--bg-hover)" : "transparent" }
      },
        h(Icons.Bell, { size: 16 }),
        h("div", {
          style: {
            position: "absolute", top: 6, right: 6,
            width: 7, height: 7, borderRadius: 99,
            background: "var(--status-warn)",
            border: "2px solid var(--bg-canvas)",
          }
        })
      ),
      // User
      h("button", {
        onClick: onOpenUserMenu,
        title: "민수 Kim",
        style: {
          display: "flex", alignItems: "center",
          padding: 2, height: 32, width: 32,
          borderRadius: 99,
          background: userMenuOpen ? "var(--bg-hover)" : "transparent",
          justifyContent: "center",
        }
      },
        h("div", { className: "avatar tone-blue", style: { width: 26, height: 26, borderRadius: 99, fontSize: 10.5 } }, "MK")
      )
    )
  );
}

/* ============================================================
 * Search Palette (⌘K)
 * ============================================================ */
function SearchPalette({ open, onClose, onNavigate }) {
  const [q, setQ] = useState("");
  const [cursor, setCursor] = useState(0);
  const inputRef = useRef(null);
  useEffect(() => {
    if (open) setTimeout(() => inputRef.current && inputRef.current.focus(), 30);
    if (!open) { setQ(""); setCursor(0); }
  }, [open]);
  useEffect(() => setCursor(0), [q]);

  if (!open) return null;

  const Data = window.Data;
  const qq = q.trim().toLowerCase();
  const matches = (s) => !qq || String(s).toLowerCase().includes(qq);

  // Build grouped sections
  const agentHits = Data.agents
    .filter(a => matches(a.name) || matches(a.desc))
    .slice(0, 6)
    .map(a => ({ kind: "Agent", label: a.name, sub: a.desc, goto: { primary: "agent", agentId: a.id }, icon: Icons.Agents, tone: a.tone }));

  const workflowHits = Data.workflows
    .filter(w => matches(w.name) || matches(w.desc))
    .slice(0, 6)
    .map(w => ({ kind: "Workflow", label: w.name, sub: w.desc, goto: { primary: "workflow", workflowId: w.id }, icon: Icons.Workflows, tone: w.tone }));

  // Sessions: full-text across transcript snippets
  const sessionHits = (Data.sessionMessages || [])
    .filter(s => matches(s.title) || matches(s.snippet) || matches(s.agent))
    .slice(0, 8)
    .map(s => {
      // Highlight context around match if query present
      let snippet = s.snippet;
      if (qq) {
        const lc = s.snippet.toLowerCase();
        const idx = lc.indexOf(qq);
        if (idx >= 0) {
          const start = Math.max(0, idx - 28);
          const end = Math.min(s.snippet.length, idx + qq.length + 60);
          snippet = (start > 0 ? "…" : "") + s.snippet.slice(start, end) + (end < s.snippet.length ? "…" : "");
        }
      }
      return {
        kind: "Session", label: s.title, sub: snippet,
        meta: `${s.agent} · ${s.when}`,
        goto: { primary: "agent", agentId: s.agentId, sessionId: s.id },
        icon: Icons.Message, tone: "slate",
      };
    });

  const sections = [
    { label: "Agents",    items: agentHits },
    { label: "Workflows", items: workflowHits },
    { label: "Sessions",  items: sessionHits, hint: qq ? null : "세션 메시지 전문 검색" },
  ].filter(s => s.items.length > 0);

  const flat = sections.flatMap(s => s.items);
  const total = flat.length;

  const commit = (it) => { onNavigate(it.goto); onClose(); };
  const onKey = (e) => {
    if (e.key === "Escape") { onClose(); return; }
    if (e.key === "ArrowDown") { e.preventDefault(); setCursor(c => Math.min(total - 1, c + 1)); }
    else if (e.key === "ArrowUp") { e.preventDefault(); setCursor(c => Math.max(0, c - 1)); }
    else if (e.key === "Enter" && flat[cursor]) { e.preventDefault(); commit(flat[cursor]); }
  };

  // Highlight helper
  const hl = (text) => {
    if (!qq || !text) return text;
    const lc = String(text).toLowerCase();
    const idx = lc.indexOf(qq);
    if (idx < 0) return text;
    return h(React.Fragment, null,
      text.slice(0, idx),
      h("mark", { style: { background: "var(--accent-blue-soft)", color: "var(--accent-blue)", padding: "0 1px", borderRadius: 2 } }, text.slice(idx, idx + qq.length)),
      text.slice(idx + qq.length)
    );
  };

  let flatIndex = -1;

  return h("div", {
    onClick: onClose,
    style: {
      position: "fixed", inset: 0, background: "var(--bg-overlay)",
      backdropFilter: "blur(2px)", zIndex: 100,
      display: "flex", alignItems: "flex-start", justifyContent: "center",
      paddingTop: 100,
    }
  },
    h("div", {
      onClick: (e) => e.stopPropagation(),
      style: {
        width: 640, background: "var(--bg-raised)",
        border: "1px solid var(--border-default)", borderRadius: 12,
        boxShadow: "var(--shadow-xl)", overflow: "hidden",
      }
    },
      h("div", {
        style: { display: "flex", alignItems: "center", gap: 10, padding: "12px 14px", borderBottom: "1px solid var(--border-subtle)" }
      },
        h(Icons.Search, { size: 16, style: { color: "var(--fg-tertiary)" } }),
        h("input", {
          ref: inputRef, value: q, onChange: e => setQ(e.target.value),
          placeholder: "Agents · Workflows · 세션 메시지 검색…",
          onKeyDown: onKey,
          style: {
            flex: 1, background: "transparent", border: "none", outline: "none",
            fontSize: 14, color: "var(--fg-primary)"
          }
        }),
        h("span", { className: "kbd" }, "esc")
      ),
      h("div", { style: { maxHeight: 440, overflowY: "auto", padding: 6 } },
        total === 0
          ? h("div", { style: { padding: 28, textAlign: "center", color: "var(--fg-muted)", fontSize: 13 } }, qq ? "결과 없음." : "입력해서 검색…")
          : sections.map(sec =>
              h("div", { key: sec.label, style: { marginBottom: 4 } },
                h("div", {
                  style: {
                    padding: "8px 10px 4px", fontSize: 10.5, fontWeight: 600,
                    color: "var(--fg-muted)", letterSpacing: "0.08em", textTransform: "uppercase",
                    display: "flex", alignItems: "center", justifyContent: "space-between"
                  }
                },
                  h("span", null, sec.label),
                  h("span", { style: { fontWeight: 500, letterSpacing: 0, textTransform: "none", color: "var(--fg-muted)" } }, sec.items.length)
                ),
                sec.items.map((it) => {
                  flatIndex++;
                  const active = flatIndex === cursor;
                  const myIdx = flatIndex;
                  return h("button", {
                    key: it.kind + it.label + myIdx,
                    onMouseEnter: () => setCursor(myIdx),
                    onClick: () => commit(it),
                    style: {
                      display: "flex", alignItems: "flex-start", gap: 10,
                      padding: "8px 10px", borderRadius: 7, width: "100%",
                      textAlign: "left",
                      background: active ? "var(--bg-hover)" : "transparent",
                      border: active ? "1px solid var(--border-subtle)" : "1px solid transparent",
                    }
                  },
                    h("div", { className: `avatar tone-${it.tone || "slate"}`, style: { width: 26, height: 26, flexShrink: 0, marginTop: 1 } },
                      h(it.icon, { size: 14 })
                    ),
                    h("div", { style: { flex: 1, minWidth: 0 } },
                      h("div", { style: { fontSize: 13, fontWeight: 500, display: "flex", alignItems: "center", gap: 6 } },
                        h("span", { className: "truncate" }, hl(it.label)),
                        it.meta && h("span", { style: { fontSize: 10.5, color: "var(--fg-muted)", fontWeight: 400, flexShrink: 0 } }, "· " + it.meta)
                      ),
                      h("div", { className: "truncate", style: { fontSize: 11.5, color: "var(--fg-tertiary)", marginTop: 2, lineHeight: 1.4 } }, hl(it.sub))
                    ),
                    active && h("span", { style: { fontSize: 10.5, color: "var(--fg-muted)", alignSelf: "center" } }, "↵")
                  );
                })
              )
            )
      ),
      h("div", {
        style: {
          display: "flex", alignItems: "center", gap: 12,
          padding: "8px 14px", borderTop: "1px solid var(--border-subtle)",
          fontSize: 11.5, color: "var(--fg-muted)"
        }
      },
        h("span", null, h("span", { className: "kbd" }, "↑"), " ", h("span", { className: "kbd" }, "↓"), " 이동"),
        h("span", null, h("span", { className: "kbd" }, "↵"), " 선택"),
        h("span", { style: { marginLeft: "auto" } }, "Aviary Search")
      )
    )
  );
}

/* ============================================================
 * User menu dropdown
 * ============================================================ */
function UserMenu({ open, onClose, theme, setTheme, onRoute }) {
  if (!open) return null;
  return h("div", {
    onClick: onClose,
    style: { position: "fixed", inset: 0, zIndex: 80 }
  },
    h("div", {
      onClick: e => e.stopPropagation(),
      style: {
        position: "absolute", top: 52, right: 14, width: 260,
        background: "var(--bg-raised)", border: "1px solid var(--border-default)",
        borderRadius: 10, boxShadow: "var(--shadow-lg)", overflow: "hidden"
      }
    },
      h("div", { style: { padding: 14, display: "flex", gap: 10, alignItems: "center", borderBottom: "1px solid var(--border-subtle)" } },
        h("div", { className: "avatar tone-blue", style: { width: 36, height: 36, fontSize: 13 } }, "MK"),
        h("div", { style: { flex: 1, minWidth: 0 } },
          h("div", { style: { fontSize: 13, fontWeight: 500 } }, "민수 Kim"),
          h("div", { style: { fontSize: 11.5, color: "var(--fg-tertiary)" }, className: "truncate" }, "minsu.kim@aviary.internal")
        ),
      ),
      h("div", { style: { padding: 4 } },
        [
          { icon: Icons.User, label: "Profile & preferences" },
          { icon: Icons.Key,  label: "API keys" },
          { icon: Icons.Sliders, label: "Model defaults" },
          { icon: Icons.Tag,  label: "Teams & access" },
        ].map((it, i) => h("button", {
          key: i,
          style: {
            display: "flex", alignItems: "center", gap: 10,
            padding: "8px 10px", width: "100%", borderRadius: 6,
            color: "var(--fg-secondary)", fontSize: 12.5, textAlign: "left",
          },
          onMouseOver: e => { e.currentTarget.style.background = "var(--bg-hover)"; e.currentTarget.style.color = "var(--fg-primary)"; },
          onMouseOut:  e => { e.currentTarget.style.background = "transparent"; e.currentTarget.style.color = "var(--fg-secondary)"; }
        },
          h(it.icon, { size: 15 }),
          h("span", null, it.label)
        ))
      ),
      h("div", { style: { borderTop: "1px solid var(--border-subtle)", padding: 8 } },
        h("div", { style: { padding: "4px 8px 8px", fontSize: 10.5, fontWeight: 600, color: "var(--fg-muted)", letterSpacing: "0.08em", textTransform: "uppercase" } }, "Appearance"),
        h("div", { style: { display: "flex", gap: 6, padding: "0 4px 4px" } },
          [
            { id: "light", label: "Light", icon: Icons.Sun },
            { id: "dark", label: "Dark", icon: Icons.Moon },
          ].map(opt => {
            const active = theme === opt.id;
            return h("button", {
              key: opt.id,
              onClick: () => setTheme(opt.id),
              style: {
                flex: 1, display: "flex", alignItems: "center", justifyContent: "center", gap: 6,
                padding: "7px 8px", borderRadius: 7,
                background: active ? "var(--accent-blue-soft)" : "transparent",
                border: `1px solid ${active ? "var(--accent-blue-border)" : "var(--border-default)"}`,
                color: active ? "var(--accent-blue)" : "var(--fg-secondary)",
                fontSize: 12, fontWeight: 500,
              }
            },
              h(opt.icon, { size: 13 }),
              h("span", null, opt.label)
            );
          })
        )
      ),
      h("div", { style: { borderTop: "1px solid var(--border-subtle)", padding: 4 } },
        h("button", {
          style: {
            display: "flex", alignItems: "center", gap: 10,
            padding: "8px 10px", width: "100%", borderRadius: 6,
            color: "var(--fg-secondary)", fontSize: 12.5, textAlign: "left",
          },
          onMouseOver: e => e.currentTarget.style.background = "var(--bg-hover)",
          onMouseOut: e => e.currentTarget.style.background = "transparent"
        },
          h(Icons.Logout, { size: 15 }),
          h("span", null, "Sign out")
        )
      )
    )
  );
}

/* ============================================================
 * Notifications dropdown — user-facing events
 * ============================================================ */
function Notifications({ open, onClose, onNavigate }) {
  if (!open) return null;
  const Data = window.Data;
  const items = Data.notifications;
  const iconFor = (kind) => kind === "chat_reply" ? Icons.Message
    : kind === "workflow_complete" ? Icons.Check
    : kind === "workflow_failed" ? Icons.X
    : Icons.Bell;
  return h("div", {
    onClick: onClose,
    style: { position: "fixed", inset: 0, zIndex: 80 }
  },
    h("div", {
      onClick: e => e.stopPropagation(),
      style: {
        position: "absolute", top: 52, right: 54, width: 360,
        background: "var(--bg-raised)", border: "1px solid var(--border-default)",
        borderRadius: 10, boxShadow: "var(--shadow-lg)", overflow: "hidden"
      }
    },
      h("div", { style: { padding: "10px 14px", borderBottom: "1px solid var(--border-subtle)", display: "flex", alignItems: "center", justifyContent: "space-between" } },
        h("div", { style: { display: "flex", alignItems: "center", gap: 8 } },
          h("div", { style: { fontSize: 13, fontWeight: 600 } }, "Notifications"),
          h("span", { className: "chip", style: { height: 18, fontSize: 10, background: "var(--accent-blue-soft)", color: "var(--accent-blue)" } },
            items.filter(i => i.unread).length + " new"
          )
        ),
        h("button", { className: "btn btn-ghost btn-sm", style: { fontSize: 11.5, padding: "0 6px", height: 22 } }, "Mark all read")
      ),
      h("div", { style: { maxHeight: 440, overflowY: "auto" } },
        items.map((it, i) => h("button", {
          key: i,
          onClick: () => {
            if (it.kind === "chat_reply" && it.agentId) onNavigate({ primary: "agent", agentId: it.agentId });
            else if (it.workflowId) onNavigate({ primary: "workflow", workflowId: it.workflowId });
            onClose();
          },
          style: {
            padding: "11px 14px", borderBottom: "1px solid var(--border-subtle)",
            display: "flex", gap: 10, width: "100%", textAlign: "left",
            background: it.unread ? "var(--accent-blue-soft)" : "transparent",
            position: "relative",
          },
          onMouseOver: e => e.currentTarget.style.background = it.unread ? "var(--accent-blue-soft)" : "var(--bg-hover)",
          onMouseOut:  e => e.currentTarget.style.background = it.unread ? "var(--accent-blue-soft)" : "transparent"
        },
          it.unread && h("span", { style: { position: "absolute", left: 5, top: 19, width: 5, height: 5, borderRadius: 99, background: "var(--accent-blue)" } }),
          h("div", { className: `avatar tone-${it.tone}`, style: { width: 28, height: 28, fontSize: 11, flexShrink: 0 } },
            h(iconFor(it.kind), { size: 13 })
          ),
          h("div", { style: { flex: 1, minWidth: 0 } },
            h("div", { style: { fontSize: 12.5, fontWeight: 500, display: "flex", alignItems: "center", gap: 6 } },
              h("span", { className: "truncate" }, it.title),
            ),
            h("div", { style: { fontSize: 11.5, color: "var(--fg-tertiary)", marginTop: 2, lineHeight: 1.4 } }, it.desc),
            h("div", { style: { fontSize: 10.5, color: "var(--fg-muted)", marginTop: 4 } }, it.when)
          )
        ))
      ),
      h("div", { style: { padding: "8px 14px", borderTop: "1px solid var(--border-subtle)", textAlign: "center" } },
        h("button", { className: "btn btn-ghost btn-sm", style: { fontSize: 11.5 } }, "View all activity")
      )
    )
  );
}

/* ============================================================
 * Reusable: breadcrumb
 * ============================================================ */
function Crumb({ children }) {
  return h("div", { style: { display: "flex", alignItems: "center", gap: 6, minWidth: 0 } }, children);
}
function CrumbLink({ children, onClick, active }) {
  return h("button", {
    onClick,
    style: {
      fontSize: 13.5, fontWeight: active ? 600 : 500,
      color: active ? "var(--fg-primary)" : "var(--fg-tertiary)",
      letterSpacing: "-0.005em",
      padding: "4px 6px", borderRadius: 5,
    },
    onMouseOver: e => { if (!active) e.currentTarget.style.color = "var(--fg-primary)"; },
    onMouseOut:  e => { if (!active) e.currentTarget.style.color = "var(--fg-tertiary)"; }
  }, children);
}
function CrumbSep() {
  return h("span", { style: { color: "var(--fg-muted)", fontSize: 11 } }, "/");
}

/* ============================================================
 * Spark line (mini)
 * ============================================================ */
function Spark({ data, color, width = 90, height = 26 }) {
  if (!data || !data.length) return null;
  const max = Math.max(...data); const min = Math.min(...data);
  const range = max - min || 1;
  const stepX = width / (data.length - 1);
  const pts = data.map((v, i) => [i * stepX, height - ((v - min) / range) * (height - 4) - 2]);
  const d = pts.map((p, i) => (i === 0 ? "M" : "L") + p[0].toFixed(1) + "," + p[1].toFixed(1)).join(" ");
  const areaD = d + ` L${width},${height} L0,${height} Z`;
  return h("svg", { width, height, style: { display: "block" } },
    h("path", { d: areaD, fill: color, opacity: 0.12 }),
    h("path", { d, fill: "none", stroke: color, strokeWidth: 1.4, strokeLinecap: "round", strokeLinejoin: "round" })
  );
}

/* ============================================================
 * Kind badge
 * ============================================================ */
function KindBadge({ kind }) {
  const config = {
    private:   { label: "Private",   bg: "var(--badge-private-bg)",   fg: "var(--badge-private-fg)",   icon: Icons.Lock },
    published: { label: "Published", bg: "var(--badge-published-bg)", fg: "var(--badge-published-fg)", icon: Icons.Globe },
    imported:  { label: "Imported",  bg: "var(--badge-imported-bg)",  fg: "var(--badge-imported-fg)",  icon: Icons.Download },
  }[kind];
  if (!config) return null;
  return h("span", {
    style: {
      display: "inline-flex", alignItems: "center", gap: 4,
      height: 19, padding: "0 7px 0 6px", borderRadius: 4,
      fontSize: 10.5, fontWeight: 600,
      background: config.bg, color: config.fg,
      letterSpacing: "0.02em",
    }
  },
    h(config.icon, { size: 10 }),
    config.label
  );
}

window.Shell = { Sidebar, Header, SearchPalette, UserMenu, Notifications, Crumb, CrumbLink, CrumbSep, Spark, KindBadge };
