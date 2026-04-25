/* global window */
// Demo data: agents, workflows, marketplace, sessions, activity

const Data = {
  user: {
    name: "민수 Kim",
    email: "minsu.kim@aviary.internal",
    initials: "MK",
    tone: "blue",
    role: "Platform Engineer",
  },

  agents: [
    // Private (내가 만든, 비공개)
    { id: "a1", name: "PR Reviewer", desc: "GitHub PR을 읽고 스타일·버그·보안 이슈를 주석으로 남깁니다.",
      kind: "private", tools: 6, sessions: 14, icon: "PR", tone: "blue", model: "Claude Sonnet 4.5",
      updated: "2h ago", lastUsed: "오늘" },
    { id: "a2", name: "SQL Explainer", desc: "쿼리 계획을 해석해 인덱스·조인·캐시 힌트를 제시합니다.",
      kind: "private", tools: 3, sessions: 42, icon: "SQ", tone: "teal", model: "Claude Sonnet 4.5",
      updated: "어제", lastUsed: "어제" },
    { id: "a3", name: "Standup Writer", desc: "Linear·Slack 활동을 모아 일일 스탠드업을 자동 작성.",
      kind: "private", tools: 4, sessions: 7, icon: "SW", tone: "amber", model: "Claude Haiku 4.5",
      updated: "3d ago", lastUsed: "3일 전" },
    // Published (내가 마켓에 올림)
    { id: "a4", name: "Log Triage", desc: "Datadog·Sentry 로그 스트림에서 원인·영향·복구 단계를 정리.",
      kind: "published", version: "v2.3.0", tools: 7, sessions: 31, icon: "LT", tone: "rose", model: "Claude Sonnet 4.5",
      updated: "2d ago", lastUsed: "오늘", installs: 142 },
    { id: "a5", name: "Docs Drafter", desc: "코드 diff로 ADR·PR 설명·릴리즈 노트를 한 번에 초안 생성.",
      kind: "published", version: "v1.4.1", tools: 5, sessions: 19, icon: "DD", tone: "purple", model: "Claude Sonnet 4.5",
      updated: "1w ago", lastUsed: "2일 전", installs: 88 },
    // Imported (남의 것을 가져옴)
    { id: "a6", name: "AWS Cost Cutter", desc: "AWS 비용 이상치를 찾아 절감 후보를 랭킹으로 제시.",
      kind: "imported", author: "@infra-team", version: "v3.0.2", tools: 8, sessions: 5, icon: "AC", tone: "green", model: "Claude Opus 4.1",
      updated: "updated v3.1 available", lastUsed: "1주 전", hasUpdate: true },
    { id: "a7", name: "Figma Extractor", desc: "Figma URL로부터 컴포넌트·토큰 메타를 뽑아 React 스펙화.",
      kind: "imported", author: "@design-ops", version: "v1.2.0", tools: 4, sessions: 2, icon: "FE", tone: "pink", model: "Claude Sonnet 4.5",
      updated: "2w ago", lastUsed: "2주 전" },
    { id: "a8", name: "Security Scanner", desc: "CVE 데이터베이스를 조회해 의존성 위험도를 점수화.",
      kind: "imported", author: "@security", version: "v2.0.0", tools: 9, sessions: 0, icon: "SS", tone: "slate", model: "Claude Sonnet 4.5",
      updated: "3w ago", lastUsed: "없음" },
  ],

  workflows: [
    { id: "w1", name: "Release Notes Pipeline", desc: "태그 → 커밋 요약 → 카테고리 분류 → Notion 발행.",
      kind: "published", category: "Release", featured: true, version: "v1.2.0", nodes: 7, runs: 124, status: "deployed", tone: "blue",
      lastRun: "12m ago", lastStatus: "completed", installs: 47 },
    { id: "w2", name: "Incident Runbook", desc: "알람 페이로드 → 분류 → 초동 대응 스크립트 실행.",
      kind: "private", category: "Ops", featured: true, nodes: 12, runs: 38, status: "deployed", tone: "rose",
      lastRun: "1h ago", lastStatus: "completed" },
    { id: "w3", name: "Data Contract Validator", desc: "스키마 diff → 영향받는 다운스트림 테스트 → 승인 요청.",
      kind: "private", category: "Data", nodes: 9, runs: 67, status: "draft", tone: "amber",
      lastRun: "어제", lastStatus: "failed" },
    { id: "w4", name: "Onboarding Kit", desc: "신입 정보 → 계정 프로비저닝 → 멘토 매칭 → 환영 메시지.",
      kind: "imported", category: "Onboarding", featured: true, author: "@people-ops", version: "v2.1.0", nodes: 11, runs: 14, status: "deployed", tone: "green",
      lastRun: "2d ago", lastStatus: "completed" },
    { id: "w5", name: "Nightly Cost Report", desc: "AWS·GCP 사용량 → 팀별 분배 → Slack 전송.",
      kind: "private", category: "Infra", nodes: 6, runs: 89, status: "deployed", tone: "amber",
      lastRun: "8h ago", lastStatus: "completed" },
    { id: "w6", name: "Embedding Index Refresh", desc: "신규 문서 → 청킹 → 임베딩 → 벡터 DB 업서트.",
      kind: "private", category: "AI", nodes: 5, runs: 312, status: "deployed", tone: "purple",
      lastRun: "1h ago", lastStatus: "completed" },
    { id: "w7", name: "Evaluation Harness", desc: "프롬프트 변경 → 회귀 테스트 스위트 실행 → 리포트.",
      kind: "imported", category: "AI", author: "@ml-platform", version: "v0.9.0", nodes: 8, runs: 24, status: "draft", tone: "teal",
      lastRun: "3d ago", lastStatus: "completed" },
    { id: "w8", name: "Bug Triage", desc: "Sentry 이슈 → 영향도 산정 → Linear 티켓 생성.",
      kind: "private", category: "Ops", nodes: 7, runs: 56, status: "deployed", tone: "rose",
      lastRun: "45m ago", lastStatus: "completed" },
  ],

  marketplace: [
    { id: "m1", name: "AWS Cost Cutter", author: "@infra-team", version: "v3.1.0", installs: "1.2k", rating: 4.8,
      category: "Infra", desc: "AWS 비용 이상치 탐지·절감 후보 랭킹.", tone: "green", imported: true, newUpdate: true },
    { id: "m2", name: "Figma Extractor", author: "@design-ops", version: "v1.2.0", installs: "840", rating: 4.6,
      category: "Design", desc: "Figma URL → 컴포넌트·토큰 추출.", tone: "pink", imported: true },
    { id: "m3", name: "Security Scanner", author: "@security", version: "v2.0.0", installs: "2.1k", rating: 4.9,
      category: "Security", desc: "CVE 매핑, 의존성 위험도 점수.", tone: "slate", imported: true },
    { id: "m4", name: "GitHub Issue Triage", author: "@dev-ex", version: "v4.2.0", installs: "3.4k", rating: 4.9,
      category: "Dev Tools", desc: "이슈를 라벨·담당자·우선순위로 자동 분류.", tone: "blue" },
    { id: "m5", name: "BigQuery Profiler", author: "@data-platform", version: "v1.0.8", installs: "612", rating: 4.4,
      category: "Data", desc: "쿼리 비용·런타임 프로파일링, 최적화 제안.", tone: "teal" },
    { id: "m6", name: "Kafka Replay", author: "@streams", version: "v0.9.2", installs: "221", rating: 4.2,
      category: "Infra", desc: "토픽에서 임의 시간 구간의 메시지를 재생.", tone: "amber" },
    { id: "m7", name: "PagerDuty Rotator", author: "@sre", version: "v2.3.0", installs: "980", rating: 4.7,
      category: "Ops", desc: "인력·시간대 제약 고려한 온콜 스케줄.", tone: "rose" },
    { id: "m8", name: "Salesforce Sync", author: "@rev-ops", version: "v3.0.0", installs: "540", rating: 4.3,
      category: "Biz", desc: "CRM 양방향 싱크, 중복 감지.", tone: "purple" },
    { id: "m9", name: "Content Moderator", author: "@trust-safety", version: "v2.2.0", installs: "1.8k", rating: 4.8,
      category: "AI", desc: "텍스트·이미지 모더레이션, 정책 룰 엔진.", tone: "blue" },
    { id: "m10", name: "Stripe Reconciler", author: "@finance", version: "v1.6.0", installs: "318", rating: 4.5,
      category: "Biz", desc: "결제·환불·분쟁을 원장과 대조.", tone: "green" },
    { id: "m11", name: "Slack Digest", author: "@comms", version: "v1.1.4", installs: "2.6k", rating: 4.6,
      category: "Comms", desc: "채널별 일간/주간 다이제스트.", tone: "amber" },
    { id: "m12", name: "Terraform Linter", author: "@platform", version: "v2.8.0", installs: "720", rating: 4.7,
      category: "Infra", desc: "모듈 구조·네이밍·권한 최소화 검사.", tone: "slate" },
  ],

  recentSessions: [
    { id: "s1", agent: "PR Reviewer", tone: "blue", title: "feat/auth-refactor PR 리뷰",
      preview: "인증 플로우에서 토큰 검증 누락 지점 3곳을 찾았습니다…", when: "12m ago", msgs: 24 },
    { id: "s2", agent: "SQL Explainer", tone: "teal", title: "users join orders 쿼리 느림",
      preview: "nested loop이 full scan으로 전환된 원인은…", when: "1h ago", msgs: 8 },
    { id: "s3", agent: "Log Triage", tone: "rose", title: "결제 실패율 급증",
      preview: "cardinality 폭주가 14:22에 시작되어 게이트웨이 재시도를 유발…", when: "3h ago", msgs: 16 },
    { id: "s4", agent: "Docs Drafter", tone: "purple", title: "v4.2 릴리즈 노트",
      preview: "Breaking changes 3건, Features 11건, Fixes 22건으로 정리됨…", when: "어제", msgs: 5 },
    { id: "s5", agent: "Standup Writer", tone: "amber", title: "Mon 스탠드업",
      preview: "어제: PR-2419, PR-2420 머지 / 오늘: 온보딩 문서 드래프트…", when: "어제", msgs: 3 },
  ],

  recentRuns: [
    { id: "r1", workflow: "Incident Runbook", tone: "rose", trigger: "webhook: alert_high_latency", status: "completed", duration: "1m 42s", when: "18m ago" },
    { id: "r2", workflow: "Release Notes Pipeline", tone: "blue", trigger: "tag: v4.2.0", status: "running", duration: "—", when: "running" },
    { id: "r3", workflow: "Data Contract Validator", tone: "amber", trigger: "schema: orders.v14", status: "failed", duration: "24s", when: "2h ago" },
    { id: "r4", workflow: "Release Notes Pipeline", tone: "blue", trigger: "tag: v4.1.3", status: "completed", duration: "58s", when: "어제" },
    { id: "r5", workflow: "Onboarding Kit", tone: "green", trigger: "hr: new_hire_j.park", status: "completed", duration: "3m 12s", when: "2d ago" },
  ],

  categories: ["All", "Dev Tools", "Infra", "Security", "Data", "Design", "Ops", "Biz", "AI", "Comms"],

  // Recent chat sessions within a specific agent (used on Agent Detail sidebar)
  agentSessions: [
    { id: "as1", title: "feat/auth-refactor PR 리뷰", when: "12m ago", pinned: true, msgs: 24 },
    { id: "as2", title: "chore/deps-bump PR 요약", when: "1h ago", msgs: 6 },
    { id: "as3", title: "fix/ratelimit-edge PR 리뷰", when: "3h ago", msgs: 12 },
    { id: "as4", title: "refactor/billing-table 검토", when: "어제", msgs: 18 },
    { id: "as5", title: "feat/exporter 컨벤션 논의", when: "어제", msgs: 9 },
    { id: "as6", title: "hotfix/memory-leak 분석", when: "2일 전", msgs: 14 },
    { id: "as7", title: "release/v4.2 코드 diff 리뷰", when: "3일 전", msgs: 33 },
    { id: "as8", title: "experimental/vector-cache", when: "1주 전", msgs: 7 },
  ],

  // Workflow runs list within a specific workflow (used on Workflow Detail sidebar)
  workflowRuns: [
    { id: "wr1", status: "running", trigger: "tag: v4.2.0", when: "now", duration: "—" },
    { id: "wr2", status: "completed", trigger: "tag: v4.1.3", when: "어제", duration: "58s" },
    { id: "wr3", status: "completed", trigger: "tag: v4.1.2", when: "2d ago", duration: "1m 04s" },
    { id: "wr4", status: "failed", trigger: "tag: v4.1.1", when: "2d ago", duration: "12s" },
    { id: "wr5", status: "completed", trigger: "tag: v4.1.0", when: "3d ago", duration: "1m 11s" },
    { id: "wr6", status: "completed", trigger: "manual", when: "1w ago", duration: "47s" },
    { id: "wr7", status: "cancelled", trigger: "manual", when: "1w ago", duration: "3s" },
  ],

  // Stats for dashboard
  stats: {
    agentRuns: { value: 284, delta: "+12%", spark: [3,5,4,6,8,7,9,11,8,12,10,14] },
    tokens:    { value: "1.24M", delta: "+4%",  spark: [12,14,13,11,16,18,17,20,22,19,21,24] },
    sessions:  { value: 46,   delta: "+8",   spark: [2,3,5,4,6,5,7,6,8,7,9,8] },
    workflowRuns: { value: 67, delta: "+21%", spark: [1,1,2,3,2,4,5,4,6,8,7,9] },
  },

  // Workflow categories (for Workflows main filter)
  workflowCategories: ["All", "Release", "Ops", "Data", "Onboarding", "AI", "Infra"],

  // Per-agent workspace tree. Keys = agent id. Each node: {name, kind: "dir"|"file", children?, ext?, lang?, size?}
  agentWorkspaces: {
    a1: [
      { name: "src", kind: "dir", expanded: true, children: [
        { name: "features", kind: "dir", expanded: true, children: [
          { name: "auth", kind: "dir", expanded: true, children: [
            { name: "token-validator.ts", kind: "file", lang: "ts", size: "2.4 KB", mutated: true },
            { name: "session-store.ts",   kind: "file", lang: "ts", size: "1.8 KB" },
            { name: "auth-guard.tsx",     kind: "file", lang: "tsx", size: "0.9 KB" },
            { name: "index.ts",           kind: "file", lang: "ts", size: "0.2 KB" },
          ]},
          { name: "billing", kind: "dir", children: [] },
          { name: "users",   kind: "dir", children: [] },
        ]},
        { name: "lib", kind: "dir", children: [
          { name: "logger.ts", kind: "file", lang: "ts", size: "0.8 KB" },
          { name: "config.ts", kind: "file", lang: "ts", size: "1.1 KB" },
        ]},
        { name: "index.ts", kind: "file", lang: "ts", size: "0.4 KB" },
      ]},
      { name: "tests", kind: "dir", children: [
        { name: "auth.spec.ts", kind: "file", lang: "ts", size: "3.2 KB" },
      ]},
      { name: "package.json", kind: "file", lang: "json", size: "1.6 KB" },
      { name: "tsconfig.json", kind: "file", lang: "json", size: "0.5 KB" },
      { name: "README.md", kind: "file", lang: "md", size: "4.1 KB" },
      { name: ".env.example", kind: "file", lang: "env", size: "0.3 KB" },
    ],
  },

  // Content for a few files (demo)
  fileContents: {
    "src/features/auth/token-validator.ts":
`import jwt from "jsonwebtoken";
import { AuthError } from "../../lib/errors";

const ALLOWED_ALGS = ["RS256", "ES256"];
const JWKS = loadJwks(process.env.JWKS_URL!);

export function validate(token: string) {
  const [h, p, s] = token.split(".");
  const header = JSON.parse(atob(h));
  if (!ALLOWED_ALGS.includes(header.alg)) {
    throw new AuthError("invalid_alg", { alg: header.alg });
  }
  const payload = JSON.parse(atob(p));
  // TODO: verify iss claim against trusted_issuers
  await verifySignature(h + "." + p, s, JWKS);
  return payload;
}
`,
    "src/features/auth/session-store.ts":
`import { kv } from "../../lib/kv";

const TTL_DAYS = 30;

export async function put(sid: string, data: unknown) {
  await kv.set(\`session:\${sid}\`, data, { ex: TTL_DAYS * 86400 });
}

export async function get(sid: string) {
  return kv.get(\`session:\${sid}\`);
}
`,
    "src/features/auth/auth-guard.tsx":
`import { useAuth } from "./use-auth";

export function AuthGuard({ children }: { children: React.ReactNode }) {
  const { user } = useAuth();
  if (!user) return <Redirect to="/signin" />;
  return <>{children}</>;
}
`,
    "README.md":
`# PR Reviewer agent workspace

Read-only mirror of the repository assigned to this agent. Writes are applied
through pull requests authored by the agent and routed through Aviary's
review pipeline.
`,
    "package.json":
`{
  "name": "pr-reviewer-sandbox",
  "version": "0.3.2",
  "private": true,
  "dependencies": {
    "jsonwebtoken": "^9.0.2"
  }
}
`,
  },

  // Full-text searchable transcript snippets (for ⌘K "Sessions" section)
  sessionMessages: [
    { id: "as1", agent: "PR Reviewer", agentId: "a1", title: "feat/auth-refactor PR 리뷰", when: "12m ago",
      snippet: "token-validator.ts:42 — 서명 검증 후 iss 클레임 비교가 빠져 있어 타 IdP 토큰이 통과될 수 있습니다." },
    { id: "as1b", agent: "PR Reviewer", agentId: "a1", title: "feat/auth-refactor PR 리뷰", when: "14m ago",
      snippet: "session-store.ts:118 — TTL 30일 상수를 config로 분리 권장." },
    { id: "s2", agent: "SQL Explainer", agentId: "a2", title: "users join orders 쿼리 느림", when: "1h ago",
      snippet: "nested loop → hash join 전환 시 예상 cost 42% 감소. orders(user_id, created_at) 인덱스 추가 권장." },
    { id: "s3", agent: "Log Triage", agentId: "a4", title: "결제 실패율 급증", when: "3h ago",
      snippet: "14:22 gateway_timeout 폭증 — downstream payment-svc의 DB 커넥션 풀 고갈이 근본 원인." },
    { id: "s4", agent: "Docs Drafter", agentId: "a5", title: "v4.2 릴리즈 노트", when: "어제",
      snippet: "Breaking 3 · Features 11 · Fixes 22. auth-token 변경이 Breaking에 포함됨." },
    { id: "as3", agent: "PR Reviewer", agentId: "a1", title: "fix/ratelimit-edge PR 리뷰", when: "3h ago",
      snippet: "rate-limit 엣지 케이스 — 윈도우 경계에서 토큰 2번 소비되는 레이스 확인." },
    { id: "as7", agent: "PR Reviewer", agentId: "a1", title: "release/v4.2 코드 diff 리뷰", when: "3일 전",
      snippet: "변경 218건 중 보안 영향 7건, breaking 3건 식별." },
  ],

  // User-facing notification events
  notifications: [
    { kind: "chat_reply", tone: "blue", agent: "PR Reviewer", agentId: "a1",
      title: "PR Reviewer 응답 완료", desc: "feat/auth-refactor PR 리뷰 — 3건 이슈 발견", when: "방금", unread: true },
    { kind: "workflow_complete", tone: "green", workflow: "Release Notes Pipeline", workflowId: "w1",
      title: "Release Notes Pipeline 완료", desc: "tag v4.2.0 · 58초 · 3 Breaking · 11 Features · 22 Fixes", when: "2m 전", unread: true },
    { kind: "workflow_failed", tone: "rose", workflow: "Data Contract Validator", workflowId: "w3",
      title: "Data Contract Validator 실패", desc: "schema orders.v14 · downstream 3건 영향", when: "1h 전", unread: true },
    { kind: "chat_reply", tone: "teal", agent: "SQL Explainer", agentId: "a2",
      title: "SQL Explainer 응답 완료", desc: "users join orders 쿼리 — 인덱스 제안 포함", when: "1h 전" },
    { kind: "workflow_complete", tone: "rose", workflow: "Incident Runbook", workflowId: "w2",
      title: "Incident Runbook 완료", desc: "webhook alert_high_latency · 1m 42s", when: "18m 전" },
    { kind: "chat_reply", tone: "purple", agent: "Docs Drafter", agentId: "a5",
      title: "Docs Drafter 응답 완료", desc: "v4.2 릴리즈 노트 초안 완성", when: "어제" },
  ],
};

window.Data = Data;
