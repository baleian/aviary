/* global React, Icons, Shell, Data */
const { useState: useStateS2 } = React;
const hS2 = React.createElement;
const { KindBadge } = Shell;

/* ============================================================
 * AGENT DETAIL — chat view w/ left sidebar (sessions of THIS agent only)
 * ============================================================ */
function AgentDetailScreen({ agentId, onRoute, sessionId }) {
  const agent = Data.agents.find(a => a.id === agentId) || Data.agents[0];
  const [active, setActive] = useStateS2(sessionId || Data.agentSessions[0].id);
  const [input, setInput] = useStateS2("");
  const [panel, setPanel] = useStateS2("workspace"); // workspace | editor

  return hS2("div", { style: { display: "flex", height: "100%", minHeight: 0 } },
    // Left — sessions sub-sidebar (agent-scoped)
    hS2("aside", {
      style: {
        width: 260, background: "var(--bg-surface)",
        borderRight: "1px solid var(--border-subtle)",
        display: "flex", flexDirection: "column", flexShrink: 0,
      }
    },
      // Agent header
      hS2("div", { style: { padding: "14px 14px 12px", borderBottom: "1px solid var(--border-subtle)" } },
        hS2("div", { style: { display: "flex", alignItems: "center", gap: 10, marginBottom: 8 } },
          hS2("button", {
            onClick: () => onRoute({ primary: "agents" }),
            className: "btn btn-ghost btn-icon btn-sm",
            title: "Back to Agents"
          }, hS2(Icons.ChevronLeft, { size: 14 })),
          hS2("div", { className: `avatar tone-${agent.tone}`, style: { width: 32, height: 32, fontSize: 12 } }, agent.icon),
          hS2("div", { style: { flex: 1, minWidth: 0 } },
            hS2("div", { style: { fontSize: 13.5, fontWeight: 600 }, className: "truncate" }, agent.name),
            hS2("div", { style: { fontSize: 11, color: "var(--fg-tertiary)" } }, agent.model)
          )
        ),
        hS2("div", { style: { display: "flex", alignItems: "center", gap: 6, marginBottom: 10 } },
          hS2(KindBadge, { kind: agent.kind }),
          agent.version && hS2("span", { className: "t-mono", style: { fontSize: 11, color: "var(--fg-tertiary)" } }, agent.version)
        ),
        hS2("button", {
          onClick: () => {}, className: "btn btn-primary btn-sm",
          style: { width: "100%", justifyContent: "center" }
        },
          hS2(Icons.Plus, { size: 13 }), "New session"
        )
      ),
      // Sessions list
      hS2("div", { style: { padding: "8px 8px", flex: 1, overflowY: "auto" } },
        hS2("div", { className: "t-over", style: { padding: "6px 8px" } }, "Recent"),
        Data.agentSessions.map(s => {
          const isActive = s.id === active;
          return hS2("button", {
            key: s.id,
            onClick: () => setActive(s.id),
            style: {
              width: "100%", textAlign: "left",
              padding: "7px 10px", borderRadius: 7, marginBottom: 1,
              background: isActive ? "var(--bg-active)" : "transparent",
              color: isActive ? "var(--fg-primary)" : "var(--fg-secondary)",
              display: "flex", alignItems: "flex-start", gap: 6,
            },
            onMouseOver: e => { if (!isActive) e.currentTarget.style.background = "var(--bg-hover)"; },
            onMouseOut: e => { if (!isActive) e.currentTarget.style.background = "transparent"; }
          },
            s.pinned && hS2(Icons.Star, { size: 10, style: { marginTop: 3, color: "var(--status-warn)", flexShrink: 0 } }),
            hS2("div", { style: { flex: 1, minWidth: 0 } },
              hS2("div", { className: "truncate", style: { fontSize: 12.5, fontWeight: isActive ? 500 : 450 } }, s.title),
              hS2("div", { style: { fontSize: 10.5, color: "var(--fg-muted)", marginTop: 1 } }, s.when + " · " + s.msgs + " msg")
            )
          );
        })
      )
    ),

    // Center — chat
    hS2("div", { style: { flex: 1, display: "flex", flexDirection: "column", minWidth: 0 } },
      // Chat header
      hS2("div", {
        style: {
          height: 44, padding: "0 16px", borderBottom: "1px solid var(--border-subtle)",
          display: "flex", alignItems: "center", gap: 12,
        }
      },
        hS2("div", { style: { flex: 1, minWidth: 0 } },
          hS2("div", { style: { fontSize: 13, fontWeight: 500 }, className: "truncate" }, Data.agentSessions.find(s => s.id === active)?.title || "New session"),
          hS2("div", { style: { fontSize: 11, color: "var(--fg-muted)" } }, "session_id: sess_7f9a · started 12m ago")
        ),
        hS2("div", { style: { display: "flex", gap: 2, background: "var(--bg-sunk)", borderRadius: 7, padding: 2 } },
          [{ id: "workspace", label: "Workspace" }, { id: "editor", label: "Editor" }].map(t => hS2("button", {
            key: t.id,
            onClick: () => setPanel(t.id),
            style: {
              padding: "4px 10px", borderRadius: 5,
              fontSize: 11.5, fontWeight: 500,
              background: panel === t.id ? "var(--bg-raised)" : "transparent",
              color: panel === t.id ? "var(--fg-primary)" : "var(--fg-tertiary)",
              boxShadow: panel === t.id ? "var(--shadow-sm)" : "none"
            }
          }, t.label))
        ),
        hS2("button", { className: "btn btn-ghost btn-icon btn-sm" }, hS2(Icons.MoreV, { size: 14 }))
      ),
      // Split: chat + workspace/editor
      hS2("div", { style: { flex: 1, display: "flex", minHeight: 0 } },
        // Chat messages
        hS2("div", { style: { flex: 1.2, display: "flex", flexDirection: "column", minWidth: 0 } },
          hS2("div", { style: { flex: 1, overflowY: "auto", padding: "20px 20px 12px" } },
            hS2(ChatMessages, { agent })
          ),
          // Composer
          hS2("div", { style: { padding: "0 16px 16px" } },
            hS2("div", {
              style: {
                border: "1px solid var(--border-default)", borderRadius: 12,
                background: "var(--bg-raised)", padding: "10px 10px 8px",
              }
            },
              hS2("textarea", {
                value: input, onChange: e => setInput(e.target.value),
                placeholder: `${agent.name}에게 메시지…  (⇧↵ 줄바꿈)`,
                rows: 2,
                style: {
                  width: "100%", border: "none", background: "transparent", outline: "none",
                  resize: "none", fontSize: 13.5, color: "var(--fg-primary)", lineHeight: 1.55
                }
              }),
              hS2("div", { style: { display: "flex", alignItems: "center", gap: 6, marginTop: 4 } },
                hS2("button", { className: "btn btn-ghost btn-icon btn-sm" }, hS2(Icons.Paperclip, { size: 14 })),
                hS2("button", { className: "btn btn-ghost btn-sm" },
                  hS2(Icons.Tool, { size: 12 }),
                  hS2("span", null, agent.tools, " tools")
                ),
                hS2("div", { style: { flex: 1 } }),
                hS2("span", { style: { fontSize: 11, color: "var(--fg-muted)" } }, agent.model),
                hS2("button", { className: "btn btn-primary btn-sm" },
                  hS2(Icons.ArrowUp, { size: 13 })
                )
              )
            )
          )
        ),
        // Vertical divider
        hS2("div", { style: { width: 1, background: "var(--border-subtle)" } }),
        // Workspace / Editor
        hS2("div", { style: { flex: 1, display: "flex", flexDirection: "column", minWidth: 0, background: "var(--bg-sunk)" } },
          panel === "workspace" ? hS2(WorkspacePanel) : hS2(EditorPanel, { agent })
        )
      )
    )
  );
}

function ChatMessages({ agent }) {
  return hS2("div", { style: { display: "flex", flexDirection: "column", gap: 18, maxWidth: 720, margin: "0 auto" } },
    hS2(UserMsg, { text: "이번 PR(#2419) 리뷰해줘. 특히 인증 플로우 쪽을 집중적으로 봐줬으면 해." }),
    hS2(AssistantMsg, { agent, blocks: [
      { kind: "text", content: "PR #2419를 받았습니다. `src/features/auth/` 아래 변경 8개 파일을 읽고 컨텍스트를 모으겠습니다." },
      { kind: "tool", name: "github.get_pull_request", status: "complete", elapsed: "420ms",
        input: `{ "number": 2419 }`,
        output: `title: "refactor: extract token validator"\nfiles_changed: 8\nadditions: +214  deletions: −142` },
      { kind: "tool", name: "github.read_file", status: "complete", elapsed: "180ms",
        input: `{ "path": "src/features/auth/token-validator.ts" }`,
        output: `export function validate(jwt: string) {\n  const [h, p, s] = jwt.split(".");\n  // ...\n}` },
      { kind: "text", content: "3건의 이슈를 확인했습니다." },
      { kind: "findings" },
    ] })
  );
}

function UserMsg({ text }) {
  return hS2("div", { style: { display: "flex", justifyContent: "flex-end" } },
    hS2("div", {
      style: {
        maxWidth: "70%", padding: "10px 14px",
        background: "var(--accent-blue-soft)", color: "var(--fg-primary)",
        border: "1px solid var(--accent-blue-border)",
        borderRadius: "12px 12px 2px 12px",
        fontSize: 13.5, lineHeight: 1.55
      }
    }, text)
  );
}

function AssistantMsg({ agent, blocks }) {
  return hS2("div", { style: { display: "flex", gap: 10 } },
    hS2("div", { className: `avatar tone-${agent.tone}`, style: { width: 28, height: 28, fontSize: 11, flexShrink: 0 } }, agent.icon),
    hS2("div", { style: { flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: 10 } },
      hS2("div", { style: { fontSize: 12, color: "var(--fg-tertiary)", fontWeight: 500 } }, agent.name),
      ...blocks.map((b, i) => {
        if (b.kind === "text") {
          return hS2("div", { key: i, style: { fontSize: 13.5, lineHeight: 1.6, color: "var(--fg-primary)" } }, b.content);
        }
        if (b.kind === "tool") {
          return hS2(ToolBlock, { key: i, block: b });
        }
        if (b.kind === "findings") {
          return hS2(FindingsBlock, { key: i });
        }
        return null;
      })
    )
  );
}

function ToolBlock({ block }) {
  const [open, setOpen] = useStateS2(false);
  return hS2("div", {
    style: {
      border: "1px solid var(--border-subtle)", borderRadius: 8,
      background: "var(--bg-sunk)", overflow: "hidden"
    }
  },
    hS2("button", {
      onClick: () => setOpen(!open),
      style: {
        width: "100%", padding: "8px 12px",
        display: "flex", alignItems: "center", gap: 8,
        textAlign: "left",
      }
    },
      hS2(open ? Icons.ChevronDown : Icons.ChevronRight, { size: 12, style: { color: "var(--fg-muted)" } }),
      hS2(Icons.Tool, { size: 13, style: { color: "var(--fg-tertiary)" } }),
      hS2("span", { className: "t-mono", style: { fontSize: 12 } }, block.name),
      hS2("span", { className: "chip", style: { height: 18, fontSize: 10.5, color: "var(--status-live)", background: "var(--status-live-soft)" } },
        hS2(Icons.Check, { size: 10 }), block.status
      ),
      hS2("span", { style: { fontSize: 11, color: "var(--fg-muted)", marginLeft: "auto" } }, block.elapsed)
    ),
    open && hS2("div", { style: { padding: "8px 12px 12px", borderTop: "1px solid var(--border-subtle)" } },
      hS2("div", { className: "t-over", style: { marginBottom: 4 } }, "Input"),
      hS2("pre", { className: "mono", style: { fontSize: 11.5, color: "var(--fg-secondary)", margin: 0, whiteSpace: "pre-wrap" } }, block.input),
      hS2("div", { className: "t-over", style: { marginTop: 10, marginBottom: 4 } }, "Output"),
      hS2("pre", { className: "mono", style: { fontSize: 11.5, color: "var(--fg-secondary)", margin: 0, whiteSpace: "pre-wrap" } }, block.output)
    )
  );
}

function FindingsBlock() {
  const findings = [
    { sev: "high",   loc: "token-validator.ts:42", title: "iss 클레임 검증 누락",
      desc: "서명 검증 후 발급자(iss) 비교가 빠져 있어 타 IdP 토큰이 통과될 수 있습니다." },
    { sev: "medium", loc: "session-store.ts:118", title: "TTL이 30일로 고정",
      desc: "환경별 다른 만료 정책을 고려해 config로 빼는 것을 권장합니다." },
    { sev: "low",    loc: "auth-guard.tsx:14", title: "타입 좁히기 누락",
      desc: "user가 null일 수 있는 분기가 조건 밖에서 참조되고 있습니다." },
  ];
  const sevColor = { high: "var(--status-error)", medium: "var(--status-warn)", low: "var(--fg-tertiary)" };
  const sevBg = { high: "var(--status-error-soft)", medium: "var(--status-warn-soft)", low: "var(--bg-hover)" };
  return hS2("div", { style: { border: "1px solid var(--border-subtle)", borderRadius: 10, overflow: "hidden" } },
    findings.map((f, i) => hS2("div", {
      key: i,
      style: { padding: "10px 12px", borderBottom: i < findings.length - 1 ? "1px solid var(--border-subtle)" : "none", background: "var(--bg-raised)" }
    },
      hS2("div", { style: { display: "flex", alignItems: "center", gap: 8, marginBottom: 4 } },
        hS2("span", {
          className: "chip",
          style: { background: sevBg[f.sev], color: sevColor[f.sev], textTransform: "uppercase", fontSize: 10, height: 18, letterSpacing: "0.06em" }
        }, f.sev),
        hS2("span", { className: "t-mono", style: { fontSize: 11.5, color: "var(--fg-tertiary)" } }, f.loc),
        hS2("span", { style: { fontSize: 13, fontWeight: 500 } }, f.title)
      ),
      hS2("div", { style: { fontSize: 12.5, color: "var(--fg-secondary)", paddingLeft: 0 } }, f.desc)
    ))
  );
}

function WorkspacePanel() {
  // In-chat workspace: file tree (left) → click opens editor (right)
  const files = [
    { path: "src/auth/token-validator.ts", kind: "diff", adds: 8, dels: 2, status: "modified",
      body: `export function validate(token: string) {
  const [h, p, s] = token.split(".");
  const header = JSON.parse(atob(h));
- if (header.alg !== "RS256") throw new Error("bad alg");
+ if (!ALLOWED_ALGS.includes(header.alg)) {
+   throw new AuthError("invalid_alg", { alg: header.alg });
+ }
  const payload = JSON.parse(atob(p));
+ // TODO: verify iss claim against trusted_issuers
  await verifySignature(h + "." + p, s, JWKS);
  return payload;
}` },
    { path: "src/auth/trusted-issuers.ts", kind: "new", adds: 24, dels: 0, status: "new",
      body: `// Generated by PR Reviewer · 2025-03-18
export const TRUSTED_ISSUERS = [
  "https://auth.aviary.internal",
  "https://accounts.google.com",
  "https://login.microsoftonline.com",
] as const;

export type TrustedIssuer = typeof TRUSTED_ISSUERS[number];

export function assertTrusted(iss: string): asserts iss is TrustedIssuer {
  if (!(TRUSTED_ISSUERS as readonly string[]).includes(iss)) {
    throw new AuthError("untrusted_issuer", { iss });
  }
}` },
    { path: "src/auth/errors.ts", kind: "diff", adds: 3, dels: 0, status: "modified",
      body: `export class AuthError extends Error {
  constructor(public code: string, public meta?: Record<string, unknown>) {
    super(code);
  }
+ static isAuthError(e: unknown): e is AuthError {
+   return e instanceof AuthError;
+ }
}` },
    { path: "tests/token-validator.test.ts", kind: "diff", adds: 12, dels: 1, status: "modified",
      body: `import { validate } from "../src/auth/token-validator";

test("rejects alg=none", () => {
- expect(() => validate(tokenWithAlg("none"))).toThrow();
+ expect(() => validate(tokenWithAlg("none"))).toThrow(AuthError);
});

+ test("rejects untrusted issuer", () => {
+   const t = sign({ iss: "https://evil.example.com" });
+   expect(() => validate(t)).toThrow(/untrusted_issuer/);
+ });` },
    { path: "FINDINGS.md", kind: "new", adds: 18, dels: 0, status: "artifact",
      body: `# PR #2419 · feat/auth-refactor

## 🔴 Blocker (1)
**Token alg allowlist** — header.alg was compared to a literal;
should use ALLOWED_ALGS.

## 🟡 Concerns (2)
- \`iss\` claim is not verified against a trusted issuer list
- AuthError has no discriminant — consumers cannot narrow safely

## 🟢 Nice (3)
- Consider extracting JWKS cache TTL to config
- Add structured logging in validate()
- Export types for public API` },
  ];

  // Tree grouping
  const tree = {};
  files.forEach(f => {
    const parts = f.path.split("/");
    const dir = parts.length > 1 ? parts.slice(0, -1).join("/") : "/";
    if (!tree[dir]) tree[dir] = [];
    tree[dir].push({ ...f, name: parts[parts.length - 1] });
  });
  const dirs = Object.keys(tree).sort();

  const [activePath, setActive] = useStateS2(files[0].path);
  const active = files.find(f => f.path === activePath) || files[0];

  const statusChip = (s) => s === "new"
    ? { label: "A", bg: "var(--status-live-soft)", fg: "var(--status-live)", title: "Added" }
    : s === "artifact"
    ? { label: "★", bg: "var(--accent-blue-soft)", fg: "var(--accent-blue)", title: "Artifact" }
    : { label: "M", bg: "var(--status-warn-soft)", fg: "var(--status-warn)", title: "Modified" };

  return hS2("div", { style: { height: "100%", display: "flex", minHeight: 0 } },
    // LEFT: file tree
    hS2("div", {
      style: {
        width: 240, flexShrink: 0,
        borderRight: "1px solid var(--border-subtle)",
        background: "var(--bg-surface)",
        display: "flex", flexDirection: "column", minHeight: 0,
      }
    },
      hS2("div", { style: { padding: "10px 12px", borderBottom: "1px solid var(--border-subtle)", display: "flex", alignItems: "center", gap: 6 } },
        hS2(Icons.Code, { size: 12, style: { color: "var(--fg-tertiary)" } }),
        hS2("span", { style: { fontSize: 11.5, fontWeight: 600, letterSpacing: "0.02em" } }, "Workspace"),
        hS2("span", { className: "chip", style: { marginLeft: "auto", height: 17, fontSize: 10 } }, files.length + " files")
      ),
      hS2("div", { style: { flex: 1, overflowY: "auto", padding: "6px 4px" } },
        dirs.map(dir => hS2("div", { key: dir, style: { marginBottom: 4 } },
          hS2("div", {
            style: {
              display: "flex", alignItems: "center", gap: 4,
              padding: "4px 8px", fontSize: 10.5, color: "var(--fg-muted)",
              fontFamily: "JetBrains Mono, monospace", letterSpacing: "0.02em",
            }
          },
            hS2(Icons.ChevronDown, { size: 10 }),
            hS2("span", null, dir === "/" ? "(root)" : dir)
          ),
          tree[dir].map(f => {
            const isActive = f.path === activePath;
            const chip = statusChip(f.status);
            return hS2("button", {
              key: f.path,
              onClick: () => setActive(f.path),
              style: {
                width: "100%", textAlign: "left",
                display: "flex", alignItems: "center", gap: 6,
                padding: "4px 8px 4px 20px", borderRadius: 5,
                background: isActive ? "var(--bg-active)" : "transparent",
                color: isActive ? "var(--fg-primary)" : "var(--fg-secondary)",
                fontSize: 11.5, fontFamily: "JetBrains Mono, monospace",
                borderLeft: isActive ? "2px solid var(--accent-blue)" : "2px solid transparent",
                marginLeft: 0,
              },
              onMouseOver: e => { if (!isActive) e.currentTarget.style.background = "var(--bg-hover)"; },
              onMouseOut: e => { if (!isActive) e.currentTarget.style.background = "transparent"; }
            },
              hS2(Icons.FileText, { size: 11, style: { color: "var(--fg-tertiary)", flexShrink: 0 } }),
              hS2("span", { className: "truncate", style: { flex: 1 } }, f.name),
              hS2("span", {
                title: chip.title,
                style: {
                  fontSize: 9.5, fontWeight: 700, padding: "0 5px",
                  borderRadius: 3, background: chip.bg, color: chip.fg,
                  fontFamily: "JetBrains Mono, monospace", flexShrink: 0,
                }
              }, chip.label)
            );
          })
        ))
      ),
      hS2("div", { style: { padding: "8px 12px", borderTop: "1px solid var(--border-subtle)", display: "flex", gap: 8, fontSize: 10.5, color: "var(--fg-muted)" } },
        hS2("span", null, "+", files.reduce((s,f)=>s+f.adds,0)),
        hS2("span", null, "−", files.reduce((s,f)=>s+f.dels,0)),
      )
    ),

    // RIGHT: editor panel
    hS2("div", { style: { flex: 1, display: "flex", flexDirection: "column", minWidth: 0, minHeight: 0 } },
      // Editor header — breadcrumb path
      hS2("div", {
        style: {
          padding: "8px 14px", borderBottom: "1px solid var(--border-subtle)",
          display: "flex", alignItems: "center", gap: 8, background: "var(--bg-surface)",
          minHeight: 36,
        }
      },
        hS2(Icons.FileText, { size: 12, style: { color: "var(--fg-tertiary)" } }),
        hS2("span", {
          className: "t-mono",
          style: { fontSize: 11.5, color: "var(--fg-secondary)" }
        },
          active.path.split("/").map((part, i, arr) => hS2(React.Fragment, { key: i },
            i > 0 && hS2("span", { style: { color: "var(--fg-muted)", margin: "0 3px" } }, "/"),
            hS2("span", { style: { color: i === arr.length - 1 ? "var(--fg-primary)" : "var(--fg-tertiary)", fontWeight: i === arr.length - 1 ? 500 : 400 } }, part)
          ))
        ),
        hS2("span", { style: { flex: 1 } }),
        active.status !== "artifact" && hS2("span", { className: "t-mono", style: { fontSize: 10.5, color: "var(--fg-muted)" } },
          active.adds > 0 && hS2("span", { style: { color: "var(--status-live)" } }, "+" + active.adds),
          active.dels > 0 && hS2("span", { style: { color: "var(--status-error)", marginLeft: 6 } }, "−" + active.dels)
        ),
        hS2("button", {
          className: "btn btn-ghost btn-icon btn-sm", title: "Copy",
          style: { height: 22, width: 22 }
        }, hS2(Icons.Copy, { size: 11 })),
        hS2("button", {
          className: "btn btn-ghost btn-icon btn-sm", title: "Open in editor",
          style: { height: 22, width: 22 }
        }, hS2(Icons.ExternalLink, { size: 11 })),
      ),

      // Body: code or markdown view
      active.status === "artifact"
        ? hS2("div", {
            style: {
              flex: 1, overflowY: "auto", padding: "18px 22px",
              background: "var(--bg-base)",
              fontSize: 13, lineHeight: 1.6, color: "var(--fg-secondary)",
            }
          },
            renderMarkdown(active.body)
          )
        : hS2("div", {
            style: {
              flex: 1, overflow: "auto",
              background: "var(--bg-sunk)",
              fontFamily: "JetBrains Mono, monospace", fontSize: 11.5, lineHeight: 1.65,
            }
          },
            hS2("table", { style: { width: "100%", borderCollapse: "collapse" } },
              hS2("tbody", null,
                active.body.split("\n").map((line, i) => {
                  const isAdd = line.startsWith("+");
                  const isDel = line.startsWith("-");
                  const bg = isAdd ? "rgba(22, 163, 74, 0.08)" : isDel ? "rgba(220, 38, 38, 0.08)" : "transparent";
                  const gutterBg = isAdd ? "var(--status-live-soft)" : isDel ? "var(--status-error-soft, rgba(220, 38, 38, 0.1))" : "var(--bg-surface)";
                  const gutterFg = isAdd ? "var(--status-live)" : isDel ? "var(--status-error)" : "var(--fg-muted)";
                  return hS2("tr", { key: i, style: { background: bg } },
                    hS2("td", {
                      style: {
                        width: 36, textAlign: "right", padding: "0 8px 0 10px",
                        color: gutterFg, background: gutterBg,
                        borderRight: "1px solid var(--border-subtle)",
                        userSelect: "none", fontSize: 10.5,
                        verticalAlign: "top",
                      }
                    }, i + 1),
                    hS2("td", {
                      style: {
                        width: 16, textAlign: "center", padding: "0 2px",
                        color: gutterFg, fontWeight: 600, userSelect: "none",
                        verticalAlign: "top",
                      }
                    }, isAdd ? "+" : isDel ? "−" : ""),
                    hS2("td", {
                      style: {
                        padding: "0 12px", color: "var(--fg-secondary)",
                        whiteSpace: "pre", verticalAlign: "top",
                      }
                    }, isAdd || isDel ? line.slice(1) : line)
                  );
                })
              )
            )
          )
    )
  );
}

// Tiny inline markdown renderer for artifact files
function renderMarkdown(md) {
  const lines = md.split("\n");
  const out = [];
  lines.forEach((l, i) => {
    if (l.startsWith("# ")) out.push(hS2("h1", { key: i, style: { fontSize: 18, fontWeight: 600, marginTop: i === 0 ? 0 : 18, marginBottom: 8, letterSpacing: "-0.01em" } }, l.slice(2)));
    else if (l.startsWith("## ")) out.push(hS2("h2", { key: i, style: { fontSize: 14, fontWeight: 600, marginTop: 16, marginBottom: 6 } }, l.slice(3)));
    else if (l.startsWith("- ")) out.push(hS2("div", { key: i, style: { paddingLeft: 16, position: "relative", marginBottom: 3 } },
      hS2("span", { style: { position: "absolute", left: 4, color: "var(--fg-muted)" } }, "•"),
      l.slice(2)
    ));
    else if (l.trim() === "") out.push(hS2("div", { key: i, style: { height: 6 } }));
    else out.push(hS2("div", { key: i, style: { marginBottom: 3 } }, l));
  });
  return out;
}

function EditorPanel({ agent }) {
  return hS2("div", { style: { padding: 16, overflowY: "auto", height: "100%" } },
    hS2("div", { style: { display: "flex", alignItems: "center", gap: 8, marginBottom: 10 } },
      hS2(Icons.Edit, { size: 14, style: { color: "var(--fg-tertiary)" } }),
      hS2("span", { className: "t-h3" }, "Agent Editor"),
      hS2("span", { style: { flex: 1 } }),
      hS2("button", { className: "btn btn-outline btn-sm" }, "Save changes")
    ),
    hS2("div", { className: "card", style: { padding: 14, marginBottom: 12 } },
      hS2("div", { className: "t-over", style: { marginBottom: 6 } }, "Name"),
      hS2("input", { className: "input", defaultValue: agent.name, style: { width: "100%" } }),
      hS2("div", { className: "t-over", style: { marginTop: 12, marginBottom: 6 } }, "Description"),
      hS2("textarea", {
        className: "input", defaultValue: agent.desc,
        rows: 2, style: { width: "100%", height: "auto", paddingTop: 8, paddingBottom: 8, lineHeight: 1.5 }
      }),
    ),
    hS2("div", { className: "card", style: { padding: 14, marginBottom: 12 } },
      hS2("div", { className: "t-over", style: { marginBottom: 8 } }, "System prompt"),
      hS2("textarea", {
        className: "input",
        defaultValue: "You are a senior code reviewer focused on security, maintainability and DX. Always anchor findings to exact file/line. Be concise.",
        rows: 6,
        style: { width: "100%", height: "auto", lineHeight: 1.55, fontFamily: "JetBrains Mono, monospace", fontSize: 12 }
      })
    ),
    hS2("div", { className: "card", style: { padding: 14, marginBottom: 12 } },
      hS2("div", { style: { display: "flex", alignItems: "center", marginBottom: 8 } },
        hS2("div", { className: "t-over" }, "Tools · " + agent.tools),
        hS2("button", { className: "btn btn-ghost btn-sm", style: { marginLeft: "auto" } },
          hS2(Icons.Plus, { size: 12 }), "Add tool"
        )
      ),
      ["github.get_pull_request", "github.read_file", "github.post_review_comment", "jira.search", "semgrep.scan", "notion.create_page"].map((t, i) => hS2("div", {
        key: t,
        style: { display: "flex", alignItems: "center", gap: 8, padding: "6px 0", borderTop: i ? "1px solid var(--border-subtle)" : "none" }
      },
        hS2(Icons.Tool, { size: 13, style: { color: "var(--fg-tertiary)" } }),
        hS2("span", { className: "t-mono", style: { fontSize: 12 } }, t),
        hS2("span", { style: { flex: 1 } }),
        hS2("button", { className: "btn btn-ghost btn-icon btn-sm" }, hS2(Icons.X, { size: 12 }))
      ))
    ),
    hS2("div", { className: "card", style: { padding: 14 } },
      hS2("div", { className: "t-over", style: { marginBottom: 8 } }, "Model"),
      hS2("div", { style: { display: "flex", gap: 8 } },
        hS2("div", { className: "input", style: { flex: 1, display: "flex", alignItems: "center", fontSize: 12.5 } }, agent.model),
        hS2("div", { className: "input", style: { width: 110, display: "flex", alignItems: "center", fontSize: 12.5 } }, "max 4096")
      )
    )
  );
}

window.Screens2 = { AgentDetailScreen };
