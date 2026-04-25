/* global React, Icons, Shell, Data */
const { useState: useStateS4, useMemo: useMemoS4 } = React;
const hS4 = React.createElement;

function MarketplaceScreen({ onRoute }) {
  const [kind, setKind] = useStateS4("agents"); // agents | workflows
  const [cat, setCat] = useStateS4("All");
  const [q, setQ] = useStateS4("");
  const [sort, setSort] = useStateS4("popular");
  const [mineOnly, setMineOnly] = useStateS4(false);
  const items = useMemoS4(() => {
    // Base list: Marketplace data is agent-like. For workflows, synthesize from Data.workflows with marketplace-ish shape.
    let r;
    if (kind === "agents") {
      r = Data.marketplace.slice();
    } else {
      r = Data.workflows.filter(w => w.kind === "published" || w.kind === "imported").map(w => ({
        id: "wf_" + w.id, name: w.name, author: w.author || "@you", version: w.version || "v1.0.0",
        installs: (w.installs || 0) + "", rating: 4.6, category: "Workflows",
        desc: w.desc, tone: w.tone, imported: w.kind === "imported",
        mine: w.kind === "published"
      }));
    }
    if (mineOnly) r = r.filter(m => m.mine || m.author === "@you" || m.author === Data.user.name);
    if (cat !== "All" && kind === "agents") r = r.filter(m => m.category === cat);
    if (q) r = r.filter(m => (m.name + " " + m.desc).toLowerCase().includes(q.toLowerCase()));
    return r;
  }, [kind, cat, q, mineOnly]);

  return hS4("div", { style: { height: "100%", display: "flex", minHeight: 0 } },
    // Category rail
    hS4("aside", {
      style: { width: 200, flexShrink: 0, borderRight: "1px solid var(--border-subtle)", background: "var(--bg-surface)", padding: "16px 8px", overflowY: "auto" }
    },
      hS4("div", { className: "t-over", style: { padding: "4px 10px 8px" } }, "Kind"),
      [
        { id: "agents", label: "Agents", icon: Icons.Agents },
        { id: "workflows", label: "Workflows", icon: Icons.Workflows },
      ].map(k => hS4("button", {
        key: k.id, onClick: () => { setKind(k.id); setCat("All"); },
        style: {
          width: "100%", textAlign: "left", padding: "7px 10px", borderRadius: 6,
          fontSize: 12.5, color: kind === k.id ? "var(--fg-primary)" : "var(--fg-secondary)",
          background: kind === k.id ? "var(--bg-active)" : "transparent", marginBottom: 1,
          display: "flex", alignItems: "center", gap: 8, fontWeight: kind === k.id ? 500 : 450
        }
      },
        hS4(k.icon, { size: 14 }),
        hS4("span", { style: { flex: 1 } }, k.label)
      )),
      hS4("div", { style: { height: 1, background: "var(--border-subtle)", margin: "12px 8px" } }),
      hS4("button", {
        onClick: () => setMineOnly(!mineOnly),
        style: {
          width: "100%", textAlign: "left", padding: "7px 10px", borderRadius: 6,
          fontSize: 12.5, color: mineOnly ? "var(--accent-blue)" : "var(--fg-secondary)",
          background: mineOnly ? "var(--accent-blue-soft)" : "transparent",
          display: "flex", alignItems: "center", gap: 8, marginBottom: 4,
          border: "1px solid " + (mineOnly ? "var(--accent-blue-border)" : "transparent"),
          fontWeight: mineOnly ? 500 : 450
        }
      },
        hS4(Icons.Upload, { size: 13 }),
        hS4("span", { style: { flex: 1 } }, "Published by me")
      ),
      hS4("div", { style: { height: 1, background: "var(--border-subtle)", margin: "12px 8px" } }),
      kind === "agents" && hS4(React.Fragment, null,
        hS4("div", { className: "t-over", style: { padding: "4px 10px 8px" } }, "Categories"),
        Data.categories.map(c => hS4("button", {
          key: c, onClick: () => setCat(c),
          style: {
            width: "100%", textAlign: "left", padding: "6px 10px", borderRadius: 6,
            fontSize: 12.5, color: cat === c ? "var(--fg-primary)" : "var(--fg-secondary)",
            background: cat === c ? "var(--bg-active)" : "transparent", marginBottom: 1,
            display: "flex", alignItems: "center"
          }
        },
          hS4("span", { style: { flex: 1 } }, c),
          hS4("span", { style: { fontSize: 10.5, color: "var(--fg-muted)", fontVariantNumeric: "tabular-nums" } },
            c === "All" ? Data.marketplace.length : Data.marketplace.filter(m => m.category === c).length)
        )),
        hS4("div", { style: { height: 1, background: "var(--border-subtle)", margin: "12px 8px" } })
      ),
      hS4("div", { className: "t-over", style: { padding: "4px 10px 8px" } }, "Sort"),
      [
        { id: "popular", label: "Popular" },
        { id: "rating", label: "Highest rated" },
        { id: "new", label: "Newest" },
        { id: "updated", label: "Recently updated" },
      ].map(s => hS4("button", {
        key: s.id, onClick: () => setSort(s.id),
        style: {
          width: "100%", textAlign: "left", padding: "6px 10px", borderRadius: 6, fontSize: 12.5,
          color: sort === s.id ? "var(--fg-primary)" : "var(--fg-secondary)",
          background: sort === s.id ? "var(--bg-active)" : "transparent"
        }
      }, s.label)),
    ),
    // List
    hS4("div", { style: { flex: 1, overflowY: "auto", minWidth: 0 } },
      hS4("div", { style: { padding: "20px 28px 12px" } },
        hS4("div", { style: { display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 12 } },
          hS4("div", null,
            hS4("div", { className: "t-hero" }, "Marketplace"),
            hS4("div", { className: "t-small fg-tertiary", style: { marginTop: 2 } }, "사내에 공유된 에이전트와 워크플로우를 탐색하고 가져옵니다.")
          ),
          hS4("button", { className: "btn btn-outline btn-sm" }, hS4(Icons.Upload, { size: 13 }), "Publish from my library")
        ),
        hS4("div", { style: { display: "flex", gap: 8 } },
          hS4("div", { style: { position: "relative", flex: 1 } },
            hS4("input", {
              className: "input", value: q, onChange: e => setQ(e.target.value),
              placeholder: "이름, 작성자, 설명으로 검색…",
              style: { width: "100%", paddingLeft: 32, height: 34, fontSize: 13 }
            }),
            hS4("div", { style: { position: "absolute", left: 10, top: 10, color: "var(--fg-muted)" } }, hS4(Icons.Search, { size: 14 }))
          ),
          hS4("button", { className: "btn btn-outline btn-sm" }, hS4(Icons.Filter, { size: 13 }), "Filters"),
        )
      ),
      hS4("div", { style: { padding: "0 28px 40px" } },
        // Hero row — featured
        cat === "All" && !mineOnly && kind === "agents" && hS4("div", { style: { marginBottom: 16 } },
          hS4("div", { className: "t-over", style: { marginBottom: 8 } }, "Featured"),
          hS4("div", { style: { display: "grid", gridTemplateColumns: "repeat(3, 1fr)", gap: 12 } },
            Data.marketplace.slice(0, 3).map(m => hS4("button", {
              key: m.id, onClick: () => onRoute({ primary: "marketplace", itemId: m.id }),
              className: "card card-hover",
              style: {
                padding: 14, textAlign: "left", display: "flex", flexDirection: "column", gap: 10,
                background: "linear-gradient(135deg, var(--accent-blue-soft), transparent 70%), var(--bg-raised)",
                cursor: "pointer"
              }
            },
              hS4("div", { style: { display: "flex", alignItems: "flex-start", gap: 10 } },
                hS4("div", { className: `avatar tone-${m.tone}`, style: { width: 40, height: 40, fontSize: 14 } },
                  hS4(Icons.Agents, { size: 18 })
                ),
                hS4("div", { style: { flex: 1, minWidth: 0 } },
                  hS4("div", { style: { fontSize: 14, fontWeight: 600, marginBottom: 1 } }, m.name),
                  hS4("div", { style: { fontSize: 11.5, color: "var(--fg-tertiary)" } },
                    hS4("span", { className: "t-mono" }, m.version),
                    hS4("span", { style: { margin: "0 6px" } }, "·"),
                    hS4("span", null, m.category)
                  )
                ),
                m.imported && hS4("span", { className: "chip", style: { height: 19, color: "var(--status-live)", background: "var(--status-live-soft)" } },
                  hS4(Icons.Check, { size: 10 }), "Imported"
                )
              ),
              hS4("div", { style: { fontSize: 12.5, color: "var(--fg-secondary)" } }, m.desc),
              hS4("div", { style: { display: "flex", gap: 12, fontSize: 11.5, color: "var(--fg-tertiary)", marginTop: "auto" } },
                hS4("span", { style: { display: "flex", alignItems: "center", gap: 4 } },
                  hS4(Icons.Star, { size: 11, style: { color: "var(--status-warn)" } }),
                  hS4("span", { className: "t-mono" }, m.rating)),
                hS4("span", { style: { display: "flex", alignItems: "center", gap: 4 } },
                  hS4(Icons.Download, { size: 11 }),
                  hS4("span", { className: "t-mono" }, m.installs)),
                hS4("span", { style: { marginLeft: "auto", color: "var(--accent-blue)" } }, "View →")
              )
            ))
          )
        ),
        // Dense list (VS Code ext style)
        hS4("div", { className: "t-over", style: { marginBottom: 8 } },
          mineOnly ? "Published by me" :
          kind === "workflows" ? (cat === "All" ? "All workflows" : cat) :
          (cat === "All" ? "All agents" : cat)
        ),
        hS4("div", { className: "card", style: { padding: 0, overflow: "hidden" } },
          items.map((m, i) => hS4("button", {
            key: m.id,
            onClick: () => onRoute({ primary: "marketplace", itemId: m.id }),
            style: {
              width: "100%", textAlign: "left", padding: "12px 14px",
              display: "flex", gap: 12, alignItems: "center",
              borderBottom: i < items.length - 1 ? "1px solid var(--border-subtle)" : "none"
            },
            onMouseOver: e => e.currentTarget.style.background = "var(--bg-hover)",
            onMouseOut:  e => e.currentTarget.style.background = "transparent"
          },
            hS4("div", { className: `avatar tone-${m.tone}`, style: { width: 36, height: 36, fontSize: 13 } },
              hS4(Icons.Agents, { size: 16 })
            ),
              hS4("div", { style: { flex: 1, minWidth: 0 } },
                hS4("div", { style: { display: "flex", alignItems: "center", gap: 8, marginBottom: 2 } },
                  hS4("span", { style: { fontSize: 13.5, fontWeight: 600 } }, m.name),
                  hS4("span", { className: "t-mono", style: { fontSize: 11, color: "var(--fg-tertiary)" } }, m.version),
                  m.newUpdate && hS4("span", { className: "chip", style: { height: 18, color: "var(--status-warn)", background: "var(--status-warn-soft)", fontSize: 10 } }, "NEW")
                ),
                hS4("div", { style: { fontSize: 12, color: "var(--fg-tertiary)" }, className: "truncate" }, m.desc)
              ),
            hS4("div", { style: { display: "flex", alignItems: "center", gap: 16, fontSize: 11.5, color: "var(--fg-tertiary)", minWidth: 220, justifyContent: "flex-end" } },
              hS4("span", { style: { display: "flex", alignItems: "center", gap: 4 } },
                hS4(Icons.Star, { size: 11, style: { color: "var(--status-warn)" } }),
                hS4("span", { className: "t-mono" }, m.rating)
              ),
              hS4("span", { style: { display: "flex", alignItems: "center", gap: 4 } },
                hS4(Icons.Download, { size: 11 }),
                hS4("span", { className: "t-mono" }, m.installs)
              ),
              hS4("span", { className: "chip", style: { height: 19 } }, m.category)
            ),
            m.imported
              ? hS4("span", { className: "btn btn-ghost btn-sm", style: { color: "var(--status-live)", cursor: "default" } },
                  hS4(Icons.Check, { size: 12 }), "Imported")
              : hS4("span", { className: "btn btn-outline btn-sm", onClick: e => e.stopPropagation() },
                  hS4(Icons.Plus, { size: 12 }), "Import")
          ))
        ),
        // Pagination
        hS4("div", { style: { display: "flex", alignItems: "center", justifyContent: "space-between", marginTop: 14 } },
          hS4("span", { style: { fontSize: 12, color: "var(--fg-muted)" } }, "Showing 1–" + items.length + " of " + items.length),
          hS4("div", { style: { display: "flex", gap: 4 } },
            hS4("button", { className: "btn btn-outline btn-sm btn-icon", disabled: true }, hS4(Icons.ChevronLeft, { size: 13 })),
            ["1","2","3","4"].map((p, i) => hS4("button", {
              key: p, className: "btn btn-sm",
              style: {
                width: 28, padding: 0, justifyContent: "center",
                background: i === 0 ? "var(--bg-active)" : "var(--bg-raised)",
                border: "1px solid var(--border-default)", color: "var(--fg-primary)",
                fontWeight: i === 0 ? 600 : 400
              }
            }, p)),
            hS4("button", { className: "btn btn-outline btn-sm btn-icon" }, hS4(Icons.ChevronRight, { size: 13 }))
          )
        )
      )
    )
  );
}

function MarketplaceItemScreen({ itemId, onRoute }) {
  const m = Data.marketplace.find(x => x.id === itemId) || Data.marketplace[0];
  return hS4("div", { style: { height: "100%", overflowY: "auto", padding: "24px 32px 48px" } },
    hS4("div", { style: { maxWidth: 1000, margin: "0 auto" } },
      hS4("button", { onClick: () => onRoute({ primary: "marketplace" }), className: "btn btn-ghost btn-sm", style: { marginBottom: 14 } },
        hS4(Icons.ChevronLeft, { size: 13 }), "Marketplace"
      ),
      hS4("div", { className: "card", style: { padding: 24, marginBottom: 16, display: "flex", gap: 20 } },
        hS4("div", { className: `avatar tone-${m.tone}`, style: { width: 72, height: 72, fontSize: 22, borderRadius: 12 } },
          hS4(Icons.Agents, { size: 32 })
        ),
        hS4("div", { style: { flex: 1 } },
          hS4("div", { style: { fontSize: 22, fontWeight: 600, letterSpacing: "-0.01em", marginBottom: 4 } }, m.name),
          hS4("div", { style: { display: "flex", alignItems: "center", gap: 10, fontSize: 12.5, color: "var(--fg-tertiary)", marginBottom: 8 } },
            hS4("span", null, "by ", m.author),
            hS4("span", null, "·"),
            hS4("span", { className: "t-mono" }, m.version),
            hS4("span", null, "·"),
            hS4("span", { className: "chip", style: { height: 19 } }, m.category),
          ),
          hS4("div", { style: { fontSize: 13.5, color: "var(--fg-secondary)", lineHeight: 1.55, marginBottom: 14 } }, m.desc),
          hS4("div", { style: { display: "flex", gap: 8 } },
            m.imported
              ? hS4("button", { className: "btn btn-outline btn-sm" }, hS4(Icons.Check, { size: 13 }), "Imported")
              : hS4("button", { className: "btn btn-primary btn-sm" }, hS4(Icons.Download, { size: 13 }), "Import agent"),
            hS4("button", { className: "btn btn-outline btn-sm" }, hS4(Icons.Play, { size: 13 }), "Try in sandbox"),
            hS4("button", { className: "btn btn-ghost btn-sm" }, hS4(Icons.Star, { size: 13 }), "Star")
          )
        ),
        hS4("div", { style: { display: "flex", gap: 16, flexDirection: "column", borderLeft: "1px solid var(--border-subtle)", paddingLeft: 20 } },
          [["Installs", m.installs], ["Rating", m.rating + " ★"], ["Updated", "4d ago"], ["License", "Internal"]].map(r => hS4("div", { key: r[0] },
            hS4("div", { className: "t-over" }, r[0]),
            hS4("div", { style: { fontSize: 14, fontWeight: 600, marginTop: 2 } }, r[1])
          ))
        )
      ),
      hS4("div", { style: { display: "grid", gridTemplateColumns: "2fr 1fr", gap: 16 } },
        hS4("div", { className: "card", style: { padding: 20 } },
          hS4("div", { className: "t-h3", style: { marginBottom: 10 } }, "Overview"),
          hS4("div", { style: { fontSize: 13.5, color: "var(--fg-secondary)", lineHeight: 1.65 } },
            "이 에이전트는 AWS 리소스 목록을 일일·주간 단위로 스캔하여 비용 이상치를 찾아냅니다. " +
            "사용량 패턴 학습을 통해 절감 후보를 신뢰도와 함께 랭킹으로 제시하고, 승인 절차를 거쳐 자동으로 리소스 스케줄을 조정합니다."
          ),
          hS4("div", { className: "t-h3", style: { marginTop: 18, marginBottom: 8 } }, "Required tools"),
          hS4("div", { style: { display: "flex", gap: 6, flexWrap: "wrap" } },
            ["aws.ec2.list_instances", "aws.cost_explorer.query", "slack.post_message", "notion.create_page"].map(t =>
              hS4("span", { key: t, className: "chip chip-outline t-mono", style: { fontSize: 11 } }, t)
            )
          ),
          hS4("div", { className: "t-h3", style: { marginTop: 18, marginBottom: 8 } }, "Changelog"),
          hS4("div", null,
            [
              { v: "v3.1.0", date: "2d ago", note: "Graviton 마이그레이션 추천 규칙 추가" },
              { v: "v3.0.2", date: "2w ago", note: "프리티어 이탈 감지 버그 수정" },
              { v: "v3.0.0", date: "1mo ago", note: "일간 스캔 엔진 리팩터링" },
            ].map(c => hS4("div", {
              key: c.v, style: { display: "flex", gap: 12, padding: "8px 0", borderTop: "1px solid var(--border-subtle)" }
            },
              hS4("span", { className: "t-mono", style: { fontSize: 12, color: "var(--fg-primary)", width: 80 } }, c.v),
              hS4("span", { style: { fontSize: 12, color: "var(--fg-secondary)", flex: 1 } }, c.note),
              hS4("span", { style: { fontSize: 11.5, color: "var(--fg-muted)" } }, c.date)
            ))
          )
        ),
        hS4("div", { style: { display: "flex", flexDirection: "column", gap: 12 } },
          hS4("div", { className: "card", style: { padding: 14 } },
            hS4("div", { className: "t-h3", style: { marginBottom: 8 } }, "Author"),
            hS4("div", { style: { display: "flex", alignItems: "center", gap: 10 } },
              hS4("div", { className: "avatar tone-green", style: { width: 32, height: 32 } }, "IT"),
              hS4("div", null,
                hS4("div", { style: { fontSize: 13, fontWeight: 500 } }, m.author),
                hS4("div", { style: { fontSize: 11.5, color: "var(--fg-tertiary)" } }, "Infrastructure Team")
              )
            )
          ),
          hS4("div", { className: "card", style: { padding: 14 } },
            hS4("div", { className: "t-h3", style: { marginBottom: 8 } }, "Compatibility"),
            [["Model", "Claude Sonnet 4.5"], ["Min. platform", "2025.04"], ["Permissions", "AWS read-only"]].map(r =>
              hS4("div", { key: r[0], style: { display: "flex", justifyContent: "space-between", padding: "6px 0", borderTop: "1px solid var(--border-subtle)" } },
                hS4("span", { style: { fontSize: 12, color: "var(--fg-tertiary)" } }, r[0]),
                hS4("span", { style: { fontSize: 12 } }, r[1])
              )
            )
          )
        )
      )
    )
  );
}

window.Screens4 = { MarketplaceScreen, MarketplaceItemScreen };
