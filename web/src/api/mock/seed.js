// The example data of the mock: an installation with four projects, as the
// Supervisor would have it in its database.

import { uuid7, seeded } from "./util.js";

const MINUTE = 60000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;
const EPOCH = Date.UTC(2026, 0, 1);

// How long a session may go without a running turn before its sandbox is put
// to sleep. Short, so that it can be watched.
export const IDLE = 90000;
// How long a sandbox may sleep before the files of its session are archived.
const ARCHIVE = 7 * DAY;

export const PERMISSIONS = [
  "project.view",
  "agents.manage",
  "connections.manage",
  "sessions.run",
  "sessions.view_all",
  "sessions.control_all",
];

// The tools that the MCP servers of the example data list, by host. Another
// host lists one tool of its own.
// More MCP servers of the storefront, as a project that has many: the slug,
// the name, the host, the transport, and the names of their tools.
export const MORE = [
  [
    "billing",
    "Billing API",
    "billing.example.com",
    "http",
    "invoices customers subscriptions",
  ],
  [
    "feature-flags",
    "Feature flags",
    "flags.example.com",
    "http",
    "flags segments",
  ],
  [
    "analytics",
    "Product analytics",
    "analytics.example.com",
    "http",
    "events funnels cohorts",
  ],
  [
    "design-files",
    "Design files",
    "design.example.com",
    "sse",
    "files frames comments",
  ],
  [
    "support-desk",
    "Support desk",
    "support.example.com",
    "http",
    "tickets macros",
  ],
  ["ci", "CI pipelines", "ci.example.com", "http", "pipelines jobs artifacts"],
  [
    "registry",
    "Container registry",
    "registry.example.com",
    "http",
    "images tags",
  ],
  [
    "status-page",
    "Status page",
    "status.example.com",
    "sse",
    "incidents components",
  ],
  [
    "on-call",
    "On-call schedule",
    "oncall.example.com",
    "http",
    "schedules escalations",
  ],
  ["wiki", "Team wiki", "wiki.example.com", "http", "pages spaces"],
  ["chat", "Team chat", "chat.example.com", "http", "channels messages"],
  ["calendar", "Calendar", "calendar.example.com", "http", "events"],
  [
    "translations",
    "Translations",
    "i18n.example.com",
    "http",
    "strings languages",
  ],
  [
    "search-index",
    "Search index",
    "search.example.com",
    "http",
    "documents indexes",
  ],
  ["cloud-costs", "Cloud costs", "costs.example.com", "sse", "reports budgets"],
  ["logs", "Logs", "logs.example.com", "http", "logs queries"],
  [
    "metrics",
    "Metrics",
    "metrics.example.com",
    "http",
    "series dashboards alerts",
  ],
  ["traces", "Traces", "traces.example.com", "http", "traces services"],
  ["load-tests", "Load tests", "load.example.com", "http", "runs scenarios"],
  [
    "payments-sandbox",
    "Payments sandbox",
    "pay-sandbox.example.com",
    "http",
    "payments refunds",
  ],
];

export const TOOLS = {
  "tracker.example.com": [
    ["search_issues", "Search issues by text, state, label and assignee."],
    ["get_issue", "Get an issue with its comments."],
    ["create_issue", "Create an issue in a project."],
    ["add_comment", "Add a comment to an issue."],
    ["update_issue", "Change the state, labels or assignee of an issue."],
  ],
  "git.example.com": [
    ["search_code", "Search code across the repositories."],
    ["get_file_contents", "Get a file of a repository at a ref."],
    ["list_pull_requests", "List the pull requests of a repository."],
    ["get_pull_request", "Get a pull request with its diff."],
    ["create_pull_request", "Open a pull request from a branch."],
    ["add_review_comment", "Comment on a line of a pull request."],
    ...[
      "list_repositories",
      "get_repository",
      "create_repository",
      "fork_repository",
      "list_branches",
      "create_branch",
      "delete_branch",
      "list_tags",
      "get_tag",
      "list_commits",
      "get_commit",
      "compare_commits",
      "create_or_update_file",
      "delete_file",
      "push_files",
      "merge_pull_request",
      "update_pull_request",
      "update_pull_request_branch",
      "request_reviewers",
      "list_pull_request_files",
      "list_pull_request_reviews",
      "submit_review",
      "list_workflow_runs",
      "get_workflow_run",
      "rerun_workflow",
      "get_job_logs",
      "list_releases",
      "create_release",
      "list_code_scanning_alerts",
      "get_code_scanning_alert",
      "list_secret_scanning_alerts",
      "list_dependabot_alerts",
      "search_repositories",
      "search_users",
      "get_me",
      "list_notifications",
      "mark_notifications_read",
    ].map((name) => {
      const words = name.replace(/_/g, " ");
      return [name, words[0].toUpperCase() + words.slice(1) + "."];
    }),
  ],
  "docs.example.com": [
    ["search_docs", "Search the documentation for integrators."],
    ["get_page", "Get a page of the documentation as Markdown."],
  ],
  "errors.example.com": [
    ["list_issues", "List the error groups of a project, newest first."],
    ["get_issue_details", "Get an error group with its latest event."],
    ["get_event", "Get one event with its stack trace."],
    ["resolve_issue", "Mark an error group as resolved."],
  ],
  "catalog.data.svc.cluster.local": [
    ["list_tables", "List the tables of the warehouse."],
    ["describe_table", "Describe the columns of a table."],
    ["search_columns", "Find columns by name or description."],
  ],
};

// A server of many lists, for each thing that it holds, a tool that lists
// them and one that gets one of them.
for (const [, name, host, , things] of MORE) {
  TOOLS[host] = things.split(" ").flatMap((thing) => [
    [`list_${thing}`, `List the ${thing} of ${name}.`],
    [`get_${thing.replace(/s$/, "")}`, `Get one of the ${thing} of ${name}.`],
  ]);
}

const OPENCODE_OPTIONS = [
  { id: "once", kind: "allow_once", name: "Allow once" },
  { id: "always", kind: "allow_always", name: "Always allow" },
  { id: "reject", kind: "reject_once", name: "Reject" },
];

export function seed(now) {
  const random = seeded(20261121);
  // The IDs count from a fixed day, so that they are the same after every
  // reload and the address of a session keeps working.
  const id = (ago) => uuid7(EPOCH - ago, random);
  const at = (ago) => new Date(now - ago).toISOString();
  const call = () =>
    "call_" +
    Array.from(
      { length: 26 },
      () => "abcdefghijklmnopqrstuvwxyz234567"[Math.floor(random() * 32)],
    ).join("");

  const db = {
    // What the chart values of the installation say.
    config: {
      idp: true,
      admins: ["aviary-admins"],
      users: ["aviary-users"],
      harnesses: ["opencode", "codex"],
      secretStoreHosts: [
        "secrets.example.com:8200",
        "openbao.aviary-data:8200",
      ],
      secretStoreKinds: ["kv-v2"],
      // How large a file of a message may be, and how many a message may
      // carry.
      maxAttachmentBytes: 64 * 1024 * 1024,
      maxAttachments: 10,
    },
    // The accounts of the identity provider. A token of the mock is the key
    // of an account.
    accounts: {
      alice: {
        subject: "6f1c2d3e-0001-4a10-9c1e-5a1d2b3c4d01",
        username: "alice@example.com",
        display_name: "Alice Kim",
        groups: ["aviary-admins", "aviary-users"],
        note: "Administrator",
      },
      carol: {
        subject: "6f1c2d3e-0003-4a10-9c1e-5a1d2b3c4d03",
        username: "carol@example.com",
        display_name: "Carol Park",
        groups: ["aviary-users", "team-a"],
        note: "Owner of Storefront",
      },
      bob: {
        subject: "6f1c2d3e-0002-4a10-9c1e-5a1d2b3c4d02",
        username: "bob@example.com",
        display_name: "Bob Lee",
        groups: ["aviary-users"],
        note: "User in Storefront",
      },
      erin: {
        subject: "6f1c2d3e-0005-4a10-9c1e-5a1d2b3c4d05",
        username: "erin@example.com",
        display_name: "Erin Cho",
        groups: ["aviary-users", "security"],
        note: "Reads every session, runs their own",
      },
      dave: {
        subject: "6f1c2d3e-0004-4a10-9c1e-5a1d2b3c4d04",
        username: "dave@example.com",
        display_name: "Dave Yoon",
        groups: ["contractors"],
        note: "In no group that may use Aviary",
      },
    },
    users: [],
    projects: [],
    members: [],
    roles: [],
    providers: [],
    mcpServers: [],
    stores: [],
    agents: [],
    sessions: [],
    turns: [],
    // The files that messages carry, each with its turn and its place in
    // the message of the turn.
    attachments: [],
    items: new Map(),
    // Turns that are left running, with the script that continues them.
    live: [],
  };

  const user = (key, ago) => {
    const account = db.accounts[key];
    const record = {
      id: id(ago),
      subject: account.subject,
      username: account.username,
      display_name: account.display_name,
      created_at: at(ago),
    };
    db.users.push(record);
    return record.id;
  };
  const alice = user("alice", 12 * DAY),
    carol = user("carol", 11 * DAY),
    bob = user("bob", 10 * DAY),
    erin = user("erin", 9 * DAY);

  db.roles = [
    ["owner", PERMISSIONS],
    [
      "editor",
      ["project.view", "agents.manage", "connections.manage", "sessions.run"],
    ],
    ["user", ["project.view", "sessions.run"]],
    ["viewer", ["project.view"]],
    ["auditor", ["project.view", "sessions.view_all"]],
  ].map(([name, permissions]) => ({
    name,
    permissions: [...permissions],
    updated_at: at(12 * DAY),
  }));

  const project = (slug, name, ago) => {
    const record = { id: id(ago), slug, name, created_at: at(ago) };
    db.projects.push(record);
    return record.id;
  };
  const storefront = project("storefront", "Storefront", 9 * DAY);
  const data = project("data-platform", "Data Platform", 7 * DAY);
  const tools = project("internal-tools", "", 4 * DAY);
  project("playground", "Playground", 2 * HOUR);

  const member = (projectID, who, grant, ago) => {
    const record = {
      id: id(ago),
      project_id: projectID,
      created_at: at(ago),
      updated_at: at(ago),
      updated_by: alice,
    };
    if (who.group) record.group = who.group;
    else record.user = who.user ? { id: who.user } : { username: who.username };
    if (Array.isArray(grant)) record.permissions = grant;
    else record.role = grant;
    db.members.push(record);
  };
  member(storefront, { user: carol }, "owner", 9 * DAY - HOUR);
  member(storefront, { user: bob }, "user", 9 * DAY - 2 * HOUR);
  member(storefront, { group: "team-a" }, "viewer", 8 * DAY);
  member(storefront, { username: "dana@example.com" }, "editor", 3 * DAY);
  member(
    storefront,
    { user: erin },
    ["project.view", "sessions.run", "sessions.view_all"],
    2 * DAY,
  );
  member(data, { group: "team-a" }, "user", 7 * DAY - HOUR);
  member(data, { user: erin }, "auditor", 6 * DAY);
  member(tools, { user: bob }, "editor", 4 * DAY - HOUR);

  const store = (projectID, name, settings, ago) => {
    const record = {
      id: id(ago),
      project_id: projectID,
      name,
      kind: "kv-v2",
      settings,
      credential: "sealed",
      created_at: at(ago),
      updated_at: at(ago),
    };
    db.stores.push(record);
    return record.id;
  };
  const secrets = store(
    storefront,
    "Platform secrets",
    {
      address: "https://secrets.example.com:8200",
      mount: "secret",
      role_id: "aviary-storefront",
    },
    8 * DAY,
  );

  const provider = (projectID, name, url, key, ago) => {
    const record = {
      id: id(ago),
      project_id: projectID,
      name,
      url,
      created_at: at(ago),
      updated_at: at(ago),
    };
    if (key === "stored") record.api_key_sealed = true;
    else if (key) record.api_key_at = key;
    db.providers.push(record);
    return record.id;
  };
  const gateway = provider(
    storefront,
    "Company gateway",
    "https://llm.example.com/v1",
    "stored",
    9 * DAY - 3 * HOUR,
  );
  const local = provider(
    storefront,
    "Local model server",
    "http://10.0.0.5:8000/v1",
    { store: secrets, path: "aviary/llm", key: "api_key" },
    8 * DAY - HOUR,
  );
  const cluster = provider(
    data,
    "In-cluster model server",
    "http://vllm.models.svc.cluster.local:8000/v1",
    null,
    7 * DAY - 2 * HOUR,
  );
  const toolsGateway = provider(
    tools,
    "Company gateway",
    "https://llm.example.com/v1",
    "stored",
    4 * DAY - 2 * HOUR,
  );

  // An MCP server, with the tools that it listed when it was saved. Its
  // headers are as the backend keeps them: a value, {sealed}, or {at}.
  const mcpServer = (projectID, slug, name, url, transport, headers, ago) => {
    const record = {
      id: id(ago),
      project_id: projectID,
      slug,
      name,
      url,
      transport,
      headers,
      tools: (TOOLS[new URL(url).hostname] ?? []).map(
        ([tool, description]) => ({ name: tool, description }),
      ),
      created_at: at(ago),
      updated_at: at(ago),
    };
    db.mcpServers.push(record);
    return record.id;
  };
  const tracker = mcpServer(
    storefront,
    "issue-tracker",
    "Issue tracker",
    "https://tracker.example.com/mcp",
    "http",
    [{ name: "authorization", sealed: true }],
    8 * DAY - 2 * HOUR,
  );
  const codeHost = mcpServer(
    storefront,
    "code-host",
    "Code host",
    "https://git.example.com/mcp",
    "http",
    [
      {
        name: "authorization",
        at: { store: secrets, path: "mcp/git", key: "token" },
      },
    ],
    7 * DAY,
  );
  const docsSearch = mcpServer(
    storefront,
    "docs",
    "Docs search",
    "https://docs.example.com/sse",
    "sse",
    [],
    6 * DAY - HOUR,
  );
  const errors = mcpServer(
    storefront,
    "errors",
    "Error monitor",
    "https://errors.example.com/mcp",
    "http",
    [
      { name: "x-api-key", sealed: true },
      { name: "x-organization", value: "storefront" },
    ],
    2 * DAY,
  );
  const catalog = mcpServer(
    data,
    "catalog",
    "Warehouse catalog",
    "http://catalog.data.svc.cluster.local:8080/mcp",
    "http",
    [],
    6 * DAY,
  );
  const more = {};
  MORE.forEach(([slug, name, host, transport], index) => {
    const headers =
      index % 5 === 4
        ? []
        : index % 3 === 0
          ? [
              {
                name: "authorization",
                at: { store: secrets, path: `mcp/${slug}`, key: "token" },
              },
            ]
          : [{ name: "authorization", sealed: true }];
    const path = transport === "sse" ? "/sse" : "/mcp";
    more[slug] = mcpServer(
      storefront,
      slug,
      name,
      `https://${host}${path}`,
      transport,
      headers,
      (5 * DAY * (index + 1)) / MORE.length,
    );
  });

  const agent = (
    projectID,
    slug,
    name,
    description,
    harness,
    providerID,
    model,
    approval,
    instructions,
    ago,
  ) => {
    const record = {
      id: id(ago),
      project_id: projectID,
      slug,
      name,
      description,
      harness,
      created_at: at(ago),
      updated_at: at(ago),
      model: { provider: providerID, name: model },
    };
    if (instructions) record.instructions = instructions;
    if (approval) record.approval = "ask";
    db.agents.push(record);
    return record.id;
  };
  const backend = agent(
    storefront,
    "backend-engineer",
    "Backend engineer",
    "Features, fixes and tests for the storefront backend.",
    "opencode",
    gateway,
    "coder-large",
    true,
    "You work on the storefront backend: TypeScript on Node 22, tests with vitest.\nKeep changes small, and say which tests you ran.",
    9 * DAY - 4 * HOUR,
  );
  const reviewer = agent(
    storefront,
    "reviewer",
    "Reviewer",
    "Reviews a branch before it is merged.",
    "codex",
    gateway,
    "reasoner-mini",
    false,
    "Review changes for correctness first, then for clarity. Quote the lines you mean.",
    8 * DAY - 2 * HOUR,
  );
  const writer = agent(
    storefront,
    "docs-writer",
    "Docs writer",
    "Writes and updates the docs for integrators.",
    "opencode",
    local,
    "coder-small",
    false,
    "",
    6 * DAY,
  );
  agent(
    storefront,
    "release-helper",
    "",
    "",
    "codex",
    gateway,
    "coder-large",
    true,
    "Prepare releases: changelog, version bump, tag. Never push without asking.",
    3 * DAY,
  );
  const analyst = agent(
    data,
    "sql-analyst",
    "SQL analyst",
    "Answers questions about the warehouse.",
    "opencode",
    cluster,
    "qwen-coder-32b",
    false,
    "Answer questions about the warehouse with SQL. Read only.",
    7 * DAY - 3 * HOUR,
  );
  agent(
    data,
    "pipeline-doctor",
    "Pipeline doctor",
    "Finds out why a pipeline run failed.",
    "opencode",
    cluster,
    "qwen-coder-32b",
    true,
    "",
    5 * DAY,
  );
  agent(
    tools,
    "helpdesk",
    "Helpdesk",
    "",
    "opencode",
    toolsGateway,
    "coder-small",
    false,
    "",
    4 * DAY - 3 * HOUR,
  );

  // The MCP servers that the agents' sessions begin with.
  const bind = (agentID, servers) => {
    db.agents.find((other) => other.id === agentID).mcp_servers = servers;
  };
  bind(backend, [tracker, codeHost, more.ci, more.logs, more["feature-flags"]]);
  bind(reviewer, [codeHost, more.ci]);
  bind(writer, [docsSearch, more.wiki, more.translations]);
  bind(analyst, [catalog]);

  // Items, as the Supervisor assembles them when a turn ends.
  const say = (text) => ({ type: "message", text });
  const think = (text) => ({ type: "thought", text });
  const plan = (...entries) => ({
    type: "plan",
    entries: entries.map(
      ([content, status = "completed", priority = "medium"]) => ({
        content,
        priority,
        status,
      }),
    ),
  });
  const bash = (command, output, status = "completed") => {
    const item = {
      type: "tool_call",
      id: call(),
      title: command,
      kind: "execute",
      status,
      input: { command, cwd: "/home/workspace" },
    };
    if (output != null)
      item.output = { output, metadata: { output, exit: 0, truncated: false } };
    return item;
  };
  const read = (path, lines, status = "completed") => {
    const item = {
      type: "tool_call",
      id: call(),
      title: path,
      kind: "read",
      status,
      input: { filePath: "/home/workspace/" + path },
    };
    if (lines != null)
      item.output = { output: lines, metadata: { truncated: false } };
    return item;
  };
  const search = (pattern, where, output) => ({
    type: "tool_call",
    id: call(),
    title: pattern,
    kind: "search",
    status: "completed",
    input: { pattern, path: "/home/workspace/" + where },
    output: { output, metadata: { truncated: false } },
  });
  const edit = (path, before, after, diff) => ({
    type: "tool_call",
    id: call(),
    title: path,
    kind: "edit",
    status: "completed",
    input: {
      filePath: "/home/workspace/" + path,
      oldString: before,
      newString: after,
    },
    output: { output: "Edit applied successfully.", metadata: { diff } },
  });
  // asked returns the approval item for a tool call that a person answered.
  const asked = (requestID, tool, optionID, by) => ({
    type: "approval",
    id: String(requestID),
    tool_call: {
      id: tool.id,
      title: tool.title,
      kind: tool.kind,
      status: "pending",
      input: tool.input,
    },
    options: OPENCODE_OPTIONS,
    outcome: "selected",
    option_id: optionID,
    answered_by: by,
  });
  // Codex reports a command without its output: only how it exited.
  const exec = (title, command, kind = "execute") => {
    const callID = call();
    return {
      type: "tool_call",
      id: callID,
      title,
      kind,
      status: "completed",
      input: { command, cwd: "/home/workspace" },
      output: { exit_code: 0 },
      meta: {
        terminal_exit: { exit_code: 0, signal: null, terminal_id: callID },
      },
    };
  };

  const session = (agentID, by, title, ago, ended) => {
    const record = {
      id: id(ago),
      agent_id: agentID,
      title,
      created_by: by,
      created_at: at(ago),
    };
    if (ended != null) record.ended_at = at(ended);
    db.sessions.push(record);
    return record;
  };
  // turn adds the next turn of a session. took is how long it ran, in ms.
  const turn = (of, text, ago, how = {}) => {
    const seq =
      db.turns.filter((other) => other.session_id === of.id).length + 1;
    const {
      state = "completed",
      took = 40000,
      items = [],
      usage,
      stop = "end_turn",
      failure,
      unstarted,
      by = of.created_by,
      // The files that the message carries: the name, what it is and the
      // size of each.
      files = [],
    } = how;
    const record = {
      id: id(ago),
      session_id: of.id,
      seq,
      state,
      text,
      created_by: by,
      created_at: at(ago),
    };
    if (!unstarted) record.started_at = at(ago - 2400);
    if (state !== "running" && state !== "queued") {
      record.ended_at = at(ago - 2400 - took);
      if (state === "completed") record.stop_reason = stop;
      if (state === "cancelled" && !unstarted) {
        record.stop_reason = "cancelled";
        record.cancel_requested_at = at(ago - 2400 - took + 700);
      }
      if (failure) record.failure = failure;
      if (usage)
        record.usage = {
          total_tokens: usage[0] + usage[1],
          input_tokens: usage[0],
          output_tokens: usage[1],
          ...(usage[2] || {}),
        };
    }
    db.turns.push(record);
    db.items.set(record.id, items);
    files.forEach(([name, media_type, size], position) =>
      db.attachments.push({
        id: id(ago),
        session_id: of.id,
        turn_id: record.id,
        position,
        name,
        media_type,
        size,
        created_by: by,
        created_at: at(ago),
      }),
    );
    return record;
  };

  // --- Storefront, Backend engineer -----------------------------------------
  const retry = session(
    backend,
    alice,
    "Add retry with backoff to the webhook sender",
    47 * MINUTE,
  );
  {
    const first = edit(
      "src/webhooks/sender.ts",
      "await post(url, body)",
      "await withRetry(() => post(url, body), { attempts: 5, base: 200 })",
      "--- a/src/webhooks/sender.ts\n+++ b/src/webhooks/sender.ts\n@@ -18,7 +18,7 @@ export async function send(event: WebhookEvent) {\n   const body = sign(event)\n-  await post(url, body)\n+  await withRetry(() => post(url, body), { attempts: 5, base: 200 })\n   metrics.sent.inc()\n }",
    );
    const tests = edit(
      "test/webhooks/sender.test.ts",
      "",
      "import { describe, expect, it, vi } from 'vitest'\n…",
      "--- /dev/null\n+++ b/test/webhooks/sender.test.ts\n@@ -0,0 +1,6 @@\n+import { describe, expect, it, vi } from 'vitest'\n+import { send } from '../../src/webhooks/sender'\n+\n+describe('send', () => {\n+  it('retries on a network error', async () => { /* … */ })\n+})",
    );
    turn(
      retry,
      "Add retry with backoff to the webhook sender and cover it with tests.",
      46 * MINUTE,
      {
        took: 72000,
        usage: [18420, 2310],
        items: [
          think(
            "The sender posts once and gives up. I will look at how it is called first.",
          ),
          plan(
            ["Read src/webhooks/sender.ts and its callers"],
            ["Add retry with exponential backoff and jitter"],
            ["Write tests for the retry"],
          ),
          read(
            "src/webhooks/sender.ts",
            "import { post } from '../http'\nimport { sign } from './sign'\n\nexport async function send(event: WebhookEvent) {\n  const url = endpoints.for(event.shop)\n  const body = sign(event)\n  await post(url, body)\n  metrics.sent.inc()\n}\n",
          ),
          search(
            "send\\(",
            "src",
            "src/orders/model.ts:88:    await send(orderPaid(order))\nsrc/checkout/session.ts:142:  await send(checkoutCompleted(session))\n",
          ),
          first,
          asked(1, first, "always", alice),
          tests,
          say(
            "I added `withRetry` with exponential backoff and jitter: five attempts, starting at 200 ms. The sender now retries on network errors and on 5xx answers, and gives up on 4xx.\n\nThe tests are in `test/webhooks/sender.test.ts`. I have not run them yet.",
          ),
        ],
      },
    );
    const run = bash(
      "npm test -- webhooks",
      " RUN  v3.2.4 /home/workspace\n\n ✓ test/webhooks/sender.test.ts (3 tests) 1834ms\n   ✓ retries on a network error 212ms\n   ✓ gives up after five attempts 1602ms\n   ✓ does not retry on 400\n\n Test Files  1 passed (1)\n      Tests  3 passed (3)\n",
    );
    turn(retry, "Run the tests.", 31 * MINUTE, {
      took: 48000,
      usage: [21034, 640],
      items: [run, asked(1, run, "once", alice), say("All three tests pass.")],
    });
    const pushing = turn(
      retry,
      "Great. Push it to a branch named retry-backoff.",
      2 * MINUTE,
      { state: "running" },
    );
    db.live.push({
      turn: pushing.id,
      script: "seedPush",
      answers: { 1: { option: "once", by: alice } },
    });
  }

  // A call of a tool of an MCP server, as OpenCode reports it and the
  // runner records it.
  const mcpCall = (server, tool, input, output) => {
    const { slug } = db.mcpServers.find((other) => other.id === server);
    return {
      type: "tool_call",
      id: call(),
      title: `${slug}_${tool}`,
      kind: "other",
      status: "completed",
      input,
      output: { output },
      mcp: { server, slug, tool },
    };
  };
  const triage = session(
    backend,
    alice,
    "Triage last night's checkout errors",
    80 * MINUTE,
  );
  // The session has the error monitor on, which its agent does not, and the
  // feature flags off, which its agent has.
  triage.mcp = { [errors]: true, [more["feature-flags"]]: false };
  {
    turn(
      triage,
      "Which checkout errors came in last night, and is there an issue for each?",
      79 * MINUTE,
      {
        took: 51000,
        usage: [14210, 980],
        files: [
          ["checkout-errors.log", "text/plain", 1843200],
          ["error-monitor-export.csv", "text/csv", 421888],
          ["image.png", "image/png", 96256],
        ],
        items: [
          think(
            "I will list the unresolved errors first, then look for issues that mention them.",
          ),
          mcpCall(
            errors,
            "list_issues",
            {
              project: "storefront",
              query: "is:unresolved checkout",
              limit: 5,
            },
            "STOREFRONT-1F2  TypeError: Cannot read properties of undefined (reading 'total')  412 events, first seen 23:40\n" +
              "STOREFRONT-1E9  WebhookTimeout: POST https://hooks.example.com timed out  88 events, first seen 02:10\n",
          ),
          mcpCall(
            tracker,
            "search_issues",
            { query: "TypeError total checkout", state: "open" },
            "#482 Checkout total is off by one cent with two coupons (open, bug, checkout)\n",
          ),
          say(
            "Two checkout errors came in last night:\n\n| Error | Events | Issue |\n| --- | --- | --- |\n| `STOREFRONT-1F2` TypeError reading `total` | 412 | none |\n| `STOREFRONT-1E9` Webhook timeout | 88 | none |\n\nIssue #482 is about the cent rounding and not about either of them. The TypeError began at 23:40, which is when `price-rounding` was deployed.",
          ),
        ],
      },
    );
    const filed = mcpCall(
      tracker,
      "create_issue",
      {
        project: "storefront",
        title: "TypeError reading 'total' in checkout since 23:40",
        labels: ["bug", "checkout"],
      },
      "Created issue #494: TypeError reading 'total' in checkout since 23:40",
    );
    turn(triage, "File an issue for the TypeError.", 72 * MINUTE, {
      took: 23000,
      usage: [16080, 412],
      items: [
        filed,
        asked(1, filed, "once", alice),
        say(
          "Filed as **#494**, with the error group and the time it began. I left the webhook timeout alone: it looks like the receiver was down.",
        ),
      ],
    });
  }

  const flaky = session(
    backend,
    alice,
    "Why does the checkout test flake on CI?",
    3 * HOUR + 20 * MINUTE,
  );
  {
    turn(
      flaky,
      "Why does the checkout test flake on CI? It passes on my machine every time.",
      3 * HOUR + 19 * MINUTE,
      {
        took: 95000,
        usage: [26110, 1904],
        items: [
          think(
            "A test that passes locally and fails on CI is usually about time, ordering or shared state. I will look for timers first.",
          ),
          search(
            "Date\\.now|setTimeout|useFakeTimers",
            "test/checkout",
            "test/checkout/total.test.ts:41:    const expires = Date.now() + 1000\ntest/checkout/total.test.ts:47:    await new Promise((r) => setTimeout(r, 1000))\n",
          ),
          read(
            "test/checkout/total.test.ts",
            "it('drops an expired coupon', async () => {\n  const expires = Date.now() + 1000\n  const cart = withCoupon(expires)\n  await new Promise((r) => setTimeout(r, 1000))\n  expect(total(cart)).toBe(100)\n})\n",
          ),
          say(
            "The test `drops an expired coupon` races the clock.\n\n- It makes a coupon that expires in 1000 ms, sleeps for 1000 ms and expects the coupon to be gone.\n- `setTimeout` may fire a millisecond early or late. On a busy CI runner the sleep sometimes ends *before* `expires`, and the coupon still counts.\n\nThe fix is to stop sleeping and control the clock:\n\n```ts\nvi.useFakeTimers()\nconst cart = withCoupon(Date.now() + 1000)\nvi.advanceTimersByTime(1001)\nexpect(total(cart)).toBe(100)\n```\n\nSay the word and I will change it.",
          ),
        ],
      },
    );
    const fix = edit(
      "test/checkout/total.test.ts",
      "await new Promise((r) => setTimeout(r, 1000))",
      "vi.advanceTimersByTime(1001)",
      "--- a/test/checkout/total.test.ts\n+++ b/test/checkout/total.test.ts\n@@ -38,9 +38,9 @@ describe('total', () => {\n   it('drops an expired coupon', async () => {\n-    const expires = Date.now() + 1000\n-    const cart = withCoupon(expires)\n-    await new Promise((r) => setTimeout(r, 1000))\n+    vi.useFakeTimers()\n+    const cart = withCoupon(Date.now() + 1000)\n+    vi.advanceTimersByTime(1001)\n     expect(total(cart)).toBe(100)\n   })",
    );
    const check = bash(
      "npm test -- checkout",
      " ✓ test/checkout/total.test.ts (9 tests) 48ms\n\n Test Files  1 passed (1)\n      Tests  9 passed (9)\n",
    );
    turn(flaky, "Fix it.", 3 * HOUR + 12 * MINUTE, {
      took: 64000,
      usage: [28992, 811],
      items: [
        fix,
        asked(1, fix, "once", alice),
        check,
        asked(2, check, "once", alice),
        say(
          "Changed and green: the test now advances a fake clock, so it no longer depends on how fast the runner is. I ran it 50 times in a loop without a failure.",
        ),
      ],
    });
  }

  const todos = session(
    backend,
    bob,
    "Summarise the open TODOs in src/cart",
    5 * HOUR,
  );
  turn(todos, "Summarise the open TODOs in src/cart.", 5 * HOUR - MINUTE, {
    took: 38000,
    usage: [9120, 540],
    items: [
      search(
        "TODO|FIXME",
        "src/cart",
        "src/cart/price.ts:27:  // TODO: round per line, not per cart\nsrc/cart/rules.ts:63:  // FIXME: bundle rule ignores quantity\nsrc/cart/store.ts:112:  // TODO: expire abandoned carts\n",
      ),
      say(
        "Three open notes in `src/cart`:\n\n| File | Line | Note |\n| --- | --- | --- |\n| `price.ts` | 27 | Round per line, not per cart |\n| `rules.ts` | 63 | The bundle rule ignores quantity |\n| `store.ts` | 112 | Expire abandoned carts |\n\nThe second one is a bug today: two bundles in one cart are priced as one.",
      ),
    ],
  });

  const rules = session(
    backend,
    alice,
    "Explain the bundle rule in the cart",
    70000,
  );
  turn(rules, "How does the bundle rule in src/cart/rules.ts work?", 65000, {
    took: 31000,
    usage: [7410, 420],
    items: [
      read(
        "src/cart/rules.ts",
        "export function bundle(lines: Line[]): Discount | null {\n  const set = lines.filter((line) => line.sku.startsWith(BUNDLE))\n  return set.length >= 3 ? { off: cheapest(set) } : null\n}\n",
      ),
      say(
        "`bundle()` looks for three or more lines whose SKU begins with the bundle prefix, and takes the cheapest of them off. It counts lines, not units: three of one item is a single line, so it gets no discount.",
      ),
    ],
  });

  const sdk = session(
    backend,
    alice,
    "Bump the payment SDK to v4 and fix what breaks",
    DAY + 2 * HOUR,
  );
  turn(
    sdk,
    "Bump the payment SDK to v4 and fix what breaks.",
    DAY + 2 * HOUR - MINUTE,
    {
      took: 81000,
      usage: [15230, 1120],
      items: [
        plan(
          ["Read the changelog of v4"],
          ["Bump the dependency"],
          ["Fix the compile errors"],
          ["Run the tests"],
        ),
        read(
          "node_modules/@pay/sdk/CHANGELOG.md",
          "## 4.0.0\n\n- `charge()` takes an options object\n- `Client` is created with `createClient()`\n",
        ),
        say(
          "v4 changes two things that we use: `charge()` now takes one options object, and the client is made with `createClient()`. Twelve call sites are affected. Shall I go ahead?",
        ),
      ],
    },
  );
  turn(sdk, "Yes, go ahead.", DAY + 100 * MINUTE, {
    state: "failed",
    took: 9000,
    failure: {
      reason: "harness_error",
      message:
        'the model endpoint answered 401 Unauthorized: {"error":{"message":"Incorrect API key provided.","type":"invalid_request_error"}}',
    },
    items: [
      think(
        "I will bump the dependency first and let the compiler list what breaks.",
      ),
      bash("npm install @pay/sdk@4", null, "in_progress"),
    ],
  });

  const review = session(
    backend,
    carol,
    "결제 웹훅 재시도 로직 검토",
    DAY + 5 * HOUR,
  );
  turn(
    review,
    "결제 웹훅 재시도 로직을 검토해 주세요. 지수 백오프가 제대로 들어갔는지, 4xx에서는 재시도하지 않는지 확인이 필요합니다.",
    DAY + 5 * HOUR - MINUTE,
    {
      took: 52000,
      usage: [17340, 902],
      files: [
        ["결제 웹훅 재시도 설계.pdf", "application/pdf", 2411724],
        ["image.png", "image/png", 184320],
        ["image (2).png", "image/png", 201728],
        ["delivery-flow.svg", "image/svg+xml", 1024],
      ],
      items: [
        read(
          "src/webhooks/retry.ts",
          "export async function withRetry<T>(run: () => Promise<T>, { attempts, base }: Options) {\n  for (let attempt = 1; ; attempt++) {\n    try { return await run() } catch (error) {\n      if (attempt === attempts || !retryable(error)) throw error\n      await sleep(base * 2 ** (attempt - 1) * (0.5 + Math.random()))\n    }\n  }\n}\n",
        ),
        say(
          "`src/webhooks/retry.ts`를 확인했습니다.\n\n- **지수 백오프**: `base * 2 ** (attempt - 1)`로 200 ms, 400 ms, 800 ms, 1.6 s 순서로 늘어납니다. 여기에 0.5~1.5배의 지터가 곱해집니다.\n- **4xx**: `retryable()`이 네트워크 오류와 5xx에만 `true`를 돌려주므로 4xx는 바로 실패합니다. 다만 `429`도 재시도하지 않는데, 이 경우는 `Retry-After`를 따라 재시도하는 편이 맞습니다.\n- **최대 횟수**: 다섯 번이고, 마지막 실패는 원래 오류를 그대로 던집니다.\n\n`429` 처리만 추가하면 될 것 같습니다. 원하시면 테스트와 함께 고치겠습니다.",
        ),
      ],
    },
  );

  const migration = session(
    backend,
    alice,
    "Draft a migration for the orders table",
    3 * DAY,
  );
  turn(
    migration,
    "Draft a migration that adds a customer_ref column to the orders table.",
    3 * DAY - MINUTE,
    {
      state: "cancelled",
      took: 14000,
      items: [
        say(
          "I will start with the current schema of the orders table and the models that map to it, then draft the migration in",
        ),
        read("db/schema.sql", null, "in_progress"),
      ],
    },
  );

  const untitled = session(backend, bob, "", 4 * DAY);
  turn(untitled, "What does src/cart/price.ts do?", 4 * DAY - MINUTE, {
    took: 21000,
    usage: [6020, 388],
    items: [
      read(
        "src/cart/price.ts",
        "export function price(cart: Cart): Money {\n  return round(sum(cart.lines.map(linePrice)))\n}\n",
      ),
      say(
        "It computes the price of a cart: the sum of its lines, rounded once at the end. `linePrice` applies the quantity and the rules of `rules.ts`.",
      ),
    ],
  });

  const envs = session(
    backend,
    alice,
    "Rename the env vars to STOREFRONT_*",
    5 * DAY,
    4 * DAY + 20 * HOUR,
  );
  turn(
    envs,
    "Rename our environment variables so that they all begin with STOREFRONT_.",
    5 * DAY - MINUTE,
    {
      took: 88000,
      usage: [22800, 1650],
      files: [["env.example", "text/plain", 612]],
      items: [
        search(
          "process\\.env\\.",
          "src",
          "src/config.ts:4:  port: process.env.PORT\nsrc/config.ts:5:  db: process.env.DATABASE_URL\nsrc/config.ts:6:  pay: process.env.PAY_KEY\n",
        ),
        say(
          "Renamed in `src/config.ts`, the Dockerfile and `README.md`: `STOREFRONT_PORT`, `STOREFRONT_DATABASE_URL`, `STOREFRONT_PAY_KEY`. The old names are still read, with a warning, so that deploys do not break.",
        ),
      ],
    },
  );
  turn(envs, "Thanks. That is all.", 5 * DAY - 6 * MINUTE, {
    took: 6000,
    usage: [23100, 40],
    items: [say("You are welcome.")],
  });

  const audit = session(
    backend,
    erin,
    "Audit the logging of card numbers",
    6 * DAY,
  );
  turn(audit, "Do we ever log card numbers?", 6 * DAY - MINUTE, {
    state: "expired",
    unstarted: true,
    took: 120000,
  });
  turn(audit, "Do we ever log card numbers?", 6 * DAY - 5 * MINUTE, {
    took: 44000,
    usage: [14200, 610],
    items: [
      search(
        "card|pan",
        "src",
        'src/checkout/session.ts:201:  log.info({ last4: card.last4 }, "card accepted")\n',
      ),
      say(
        "No. The only log line that mentions a card is in `src/checkout/session.ts`, and it logs `last4` only. The payment SDK never hands us the full number.",
      ),
    ],
  });

  const OLD = [
    "Add an index for orders by customer",
    "Explain the retry policy of the email worker",
    "Upgrade eslint to v9",
    "Write a README for the fixtures",
    "Find where the coupon rounding goes wrong",
    "Speed up the cart API tests",
    "Remove the dead feature flag checkout_v1",
    "Add OpenTelemetry spans to the payment client",
    "Port the price rules to the new tax table",
    "Why is the bundle 40 kB bigger?",
    "Generate types from the OpenAPI spec",
    "Make the seed script idempotent",
    "Tidy the Dockerfile layers",
    "Draft release notes for 2.14",
    "Check the cron expression of the nightly job",
    "Add a health endpoint to the worker",
    "Investigate the 502s on /orders",
    "Replace moment with date-fns",
  ];
  OLD.forEach((title, index) => {
    const ago = (7 + index * 1.7) * DAY + (index % 5) * HOUR;
    const old = session(backend, [alice, bob, carol][index % 3], title, ago);
    turn(old, title + ".", ago - MINUTE, {
      took: 30000 + index * 3100,
      usage: [8000 + index * 730, 300 + index * 41],
      items: [
        say("Done. I kept the change small and ran the tests that cover it."),
      ],
    });
  });

  // --- Storefront, Reviewer (Codex) -------------------------------------------
  const pr = session(
    reviewer,
    alice,
    "Review the idempotency keys change",
    26 * HOUR,
  );
  turn(
    pr,
    "Review the change on the branch idempotency-keys.",
    26 * HOUR - MINUTE,
    {
      took: 67000,
      usage: [
        31200,
        1480,
        {
          thought_tokens: 2210,
          cached_read_tokens: 18944,
          cached_write_tokens: 0,
        },
      ],
      items: [
        exec(
          "Run git diff main...idempotency-keys --stat",
          "git diff main...idempotency-keys --stat",
        ),
        exec("Read keys.ts", "sed -n 1,80p src/checkout/keys.ts", "read"),
        exec(
          "Search for 'idempotency' in src",
          "rg -n idempotency src",
          "search",
        ),
        say(
          "Two things before this merges:\n\n1. `src/checkout/keys.ts:34` stores the key **after** the charge. A crash between the two charges the customer and forgets the key. Store it first, in the same transaction as the order.\n2. The key expires after 24 h, but the client retries for up to 72 h. Use the longer of the two.\n\nThe rest reads well. The tests cover the replay but not the crash.",
        ),
      ],
    },
  );
  const walk = session(
    reviewer,
    alice,
    "Walk through the checkout module, file by file",
    2 * DAY + 3 * HOUR,
  );
  [
    "session.ts",
    "total.ts",
    "tax.ts",
    "keys.ts",
    "coupons.ts",
    "address.ts",
    "shipping.ts",
    "stock.ts",
    "receipt.ts",
    "refund.ts",
    "limits.ts",
    "currency.ts",
    "rounding.ts",
    "errors.ts",
    "events.ts",
    "metrics.ts",
    "audit.ts",
    "flags.ts",
    "retry.ts",
    "locks.ts",
    "cache.ts",
    "guards.ts",
    "types.ts",
    "index.ts",
  ].forEach((file, index) => {
    turn(
      walk,
      index === 0
        ? "Walk me through src/checkout, one file per turn. Start with session.ts."
        : `Next: ${file}`,
      2 * DAY + 3 * HOUR - (index + 1) * 4 * MINUTE,
      {
        took: 20000 + index * 900,
        usage: [
          12000 + index * 1500,
          260,
          {
            thought_tokens: 0,
            cached_read_tokens: 9000 + index * 1400,
            cached_write_tokens: 0,
          },
        ],
        items: [
          exec("Read " + file, `sed -n 1,200p src/checkout/${file}`, "read"),
          say(
            `\`${file}\` is ${40 + index * 13} lines. It does one thing, and its only caller outside the module is \`session.ts\`.`,
          ),
        ],
      },
    );
  });
  const cart = session(reviewer, bob, "Review my cart refactor", 30 * HOUR);
  turn(
    cart,
    "Review my cart refactor on the branch cart-store.",
    30 * HOUR - MINUTE,
    {
      took: 41000,
      usage: [
        19870,
        720,
        { thought_tokens: 960, cached_read_tokens: 0, cached_write_tokens: 0 },
      ],
      items: [
        exec("Run git diff main...cart-store", "git diff main...cart-store"),
        say(
          "It is a clean move of the state into `store.ts`. One regression: `expire()` is no longer called from the cron entry point, so abandoned carts stay forever.",
        ),
      ],
    },
  );

  // --- Storefront, Docs writer --------------------------------------------------
  const docs = session(
    writer,
    carol,
    "Document the webhook retry policy",
    20 * HOUR,
  );
  turn(
    docs,
    "Document the webhook retry policy for our integrators.",
    20 * HOUR - MINUTE,
    {
      took: 58000,
      usage: [9800, 1340],
      items: [
        read(
          "src/webhooks/retry.ts",
          "export async function withRetry<T>(...) { /* … */ }\n",
        ),
        say(
          "I wrote `docs/webhooks/retries.md`:\n\n## Retries\n\nWe retry a delivery up to **five times**, with exponential backoff that starts at 200 ms and doubles each time, plus jitter.\n\n- Network errors and `5xx` answers are retried.\n- `4xx` answers are final. Fix the endpoint and replay the event from the dashboard.\n\n> Answer within 10 seconds. A slower answer counts as a failure.",
        ),
      ],
    },
  );

  // --- Data Platform ------------------------------------------------------------
  const churn = session(analyst, carol, "Weekly churn by plan", 8 * HOUR);
  turn(
    churn,
    "Show me weekly churn by plan for the last quarter.",
    8 * HOUR - MINUTE,
    {
      took: 73000,
      usage: [11200, 980],
      // As many as a message carries.
      files: [
        ["subscriptions-2026-q3.csv", "text/csv", 48234496],
        ["cancellations-2026-q3.csv", "text/csv", 6815744],
        [
          "cancellations by plan and region, export for the board (final) v3.xlsx",
          "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
          912384,
        ],
        [
          "2026년 3분기 해지 사유 설문 원본.xlsx",
          "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
          1310720,
        ],
        ["plans.json", "application/json", 2048],
        ["README.txt", "text/plain", 740],
        ["churn-dashboard-2026-11-24-at-10.32.15.png", "image/png", 734003],
        ["funnel.png", "image/png", 208896],
        ["Q3 board deck.pptx", "application/octet-stream", 15728640],
        ["notes.md", "text/markdown", 3187],
      ],
      items: [
        bash(
          "psql -c \"select plan, date_trunc('week', cancelled_at) w, count(*) from subscriptions where cancelled_at > now() - interval '3 months' group by 1, 2 order by 2, 1\"",
          " plan  |     w      | count\n-------+------------+-------\n basic | 2026-08-24 |    41\n pro   | 2026-08-24 |    12\n basic | 2026-08-31 |    38\n pro   | 2026-08-31 |     9\n(26 rows)\n",
        ),
        say(
          "Churn is flat on **pro** (9 to 12 a week) and falling on **basic**, from 41 to 27 a week over the quarter. The full table is in the output above.",
        ),
      ],
    },
  );

  // The sandbox of a session is awake while a turn runs and for a while
  // after, and asleep from then on. After a long sleep the session is
  // archived. An ended session has none.
  for (const record of db.sessions) {
    const turns = db.turns.filter(
      (other) => other.session_id === record.id && other.started_at,
    );
    if (record.ended_at || !turns.length) continue;
    const idle = Math.min(
      ...turns.map((other) =>
        other.ended_at ? now - Date.parse(other.ended_at) : 0,
      ),
    );
    record.sandbox =
      idle < IDLE ? "awake" : idle < IDLE + ARCHIVE ? "asleep" : "archived";
  }
  return db;
}
