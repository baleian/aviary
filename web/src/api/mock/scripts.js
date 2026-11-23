// What the agents of the mock do in a turn. No model is behind them: each
// script plays the events that a harness would send, in the shapes that the
// OpenCode and Codex harnesses of this repository really send them.
//
// A script gets a context from the backend (see run() in backend.js):
//
//   await ctx.pause(ms)                     waits, and throws if the turn is cancelled
//   await ctx.think(text)                   thought.delta events
//   await ctx.say(text)                     message.delta events
//   await ctx.plan([[content, status]...])  a plan.update event
//   await ctx.tool({...})                   a tool call, with approval if the agent asks
//   await ctx.mcp(server, tool, {...})      a call of a tool of an MCP server
//   ctx.servers                             the MCP servers of the turn
//   ctx.fail(reason, message)               ends the turn as failed

const quote = (text) =>
  text.length > 140 ? text.slice(0, 139).trimEnd() + "…" : text;

const hostOf = (server) => new URL(server.url).hostname;

// tools tells which MCP servers the turn has, and their tools.
async function tools(ctx) {
  await ctx.think("The question is what I can use in this session.");
  if (!ctx.servers.length) {
    await ctx.say(
      "This session has no MCP servers, so I have the tools of the harness only: reading and changing files, and running commands in the workspace.",
    );
    return;
  }
  const lines = ctx.servers.map(
    (server) =>
      `- **${server.name || server.slug}** (\`${server.slug}\`): ` +
      (server.tools ?? []).map((tool) => `\`${tool.name}\``).join(", "),
  );
  await ctx.say(
    "Besides the tools of the harness, this session gives me these MCP servers:\n\n" +
      lines.join("\n") +
      "\n\nAsk me to look something up in one of them.",
  );
}

// What the agents of the mock look up in an MCP server, by its host: the
// words of a request that call for it, and the calls that they make.
const USES = [
  {
    host: "tracker.example.com",
    found:
      "three open checkout issues. #482, the total that is off by one cent, is the one that customers see.",
    what: "the issue tracker",
    words: /\b(issues?|tickets?|bugs?)\b/i,
    async run(ctx, server) {
      await ctx.mcp(server, "search_issues", {
        input: { query: "checkout", state: "open", limit: 3 },
        output:
          "#482 Checkout total is off by one cent with two coupons (open, bug, checkout)\n" +
          "#471 Expired coupon still applied on CI (open, flaky-test)\n" +
          "#455 Checkout session times out after 30 minutes (open, checkout)\n",
      });
      if (!/\b(create|file|open|report|write)\b/i.test(ctx.turn.text)) return;
      await ctx.mcp(server, "create_issue", {
        asks: true,
        input: {
          project: "storefront",
          title: "Checkout webhooks fail without a retry",
          labels: ["bug", "webhooks"],
        },
        output: "Created issue #493: Checkout webhooks fail without a retry",
      });
    },
  },
  {
    host: "errors.example.com",
    found:
      "three unresolved error groups. STOREFRONT-1F2 is the busiest, with 412 events in the last hour.",
    what: "the error monitor",
    words: /\b(errors?|exceptions?|crash(es)?|5\d\ds?)\b/i,
    async run(ctx, server) {
      await ctx.mcp(server, "list_issues", {
        input: { project: "storefront", query: "is:unresolved", limit: 3 },
        output:
          "STOREFRONT-1F2  TypeError: Cannot read properties of undefined (reading 'total')  412 events, last 6 min ago\n" +
          "STOREFRONT-1E9  WebhookTimeout: POST https://hooks.example.com timed out  88 events, last 1 h ago\n" +
          "STOREFRONT-1C4  RangeError: Invalid time value  9 events, last 5 h ago\n",
      });
    },
  },
  {
    host: "docs.example.com",
    found:
      "three pages. *Webhooks › Retries* describes what the sender does today.",
    what: "the documentation",
    words: /\b(docs?|documentation|guides?)\b/i,
    async run(ctx, server) {
      await ctx.mcp(server, "search_docs", {
        input: { query: quote(ctx.turn.text) },
        output:
          "Webhooks › Retries — /webhooks/retries\nWebhooks › Signing payloads — /webhooks/signing\nCheckout › Coupons — /checkout/coupons\n",
      });
    },
  },
  {
    host: "git.example.com",
    found:
      "two open pull requests. #211 adds the retry with backoff to the webhook sender.",
    what: "the code host",
    words: /\b(pull requests?|prs?|reviews?|branch(es)?)\b/i,
    async run(ctx, server) {
      await ctx.mcp(server, "list_pull_requests", {
        input: { repository: "shop/storefront", state: "open" },
        output:
          "#211 Add retry with backoff to the webhook sender (retry-backoff → main)\n#207 Round prices per line (price-rounding → main)\n",
      });
    },
  },
  {
    host: "catalog.data.svc.cluster.local",
    found: "three tables in the `analytics` schema.",
    what: "the warehouse catalog",
    words: /\b(tables?|columns?|schemas?|warehouse)\b/i,
    async run(ctx, server) {
      await ctx.mcp(server, "list_tables", {
        input: { schema: "analytics" },
        output: "analytics.orders\nanalytics.order_lines\nanalytics.sessions\n",
      });
    },
  },
];

// lookUp uses the MCP servers that a request calls for, if the turn has them.
// It reports whether the request called for one.
async function lookUp(ctx) {
  const wanted = USES.filter((use) => use.words.test(ctx.turn.text));
  if (!wanted.length) return false;
  const usable = wanted
    .map((use) => [
      use,
      ctx.servers.find((server) => hostOf(server) === use.host),
    ])
    .filter(([, server]) => server);
  if (!usable.length) {
    await ctx.think(
      `The request needs ${wanted[0].what}, and no tool of this session reaches it.`,
    );
    await ctx.say(
      `I have no tool that reaches ${wanted[0].what} in this session. Turn on an MCP server for it, and ask me again.`,
    );
    return true;
  }
  await ctx.think(
    `The request needs ${usable.map(([use]) => use.what).join(" and ")}. I will look there first.`,
  );
  for (const [use, server] of usable) await use.run(ctx, server);
  await ctx.say(
    "Here is what I found:\n\n" +
      usable
        .map(
          ([use, server]) =>
            `- **${server.name || server.slug}**: ${use.found}`,
        )
        .join("\n") +
      "\n\nTell me which one to work on, and I will take it from there.",
  );
  return true;
}

async function answer(ctx) {
  if (/\btools?\b/i.test(ctx.turn.text)) return tools(ctx);
  await ctx.think("A short question. No tools are needed for this.");
  await ctx.say(
    `You said: “${quote(ctx.turn.text)}”\n\n` +
      "This page is answered from example data, so no model is behind this agent and the reply is scripted. " +
      "Everything around it is what the API does: the message became a **turn**, the turn was taken from the queue, " +
      "its events were streamed to this page, and its record was stored when it ended.\n\n" +
      "To see the agent work with tools, send a request of a sentence or more that is not a question. " +
      "An agent that asks before it acts then waits for approval.",
  );
}

const LISTING =
  ".\n..\n.git\n.github\nNOTES.md\nREADME.md\npackage.json\nsrc\ntest\ntsconfig.json\n";
const README =
  "# storefront\n\nThe storefront backend: catalogue, cart, checkout and webhooks.\n\n## Develop\n\n    npm install\n    npm test\n";
const TESTS =
  " RUN  v3.2.4 /home/workspace\n\n" +
  " ✓ test/cart/price.test.ts (14 tests) 61ms\n" +
  " ✓ test/checkout/total.test.ts (9 tests) 48ms\n" +
  " ✓ test/webhooks/sender.test.ts (3 tests) 1834ms\n\n" +
  " Test Files  3 passed (3)\n      Tests  26 passed (26)\n   Duration  2.71s\n";

// work looks around, changes a file and runs the tests. If the agent asks
// before it acts, the change and the command each wait for approval.
async function work(ctx) {
  if (await lookUp(ctx)) return;
  const note = `- ${new Date().toISOString().slice(0, 10)}: ${quote(ctx.turn.text)}`;
  await ctx.think(
    `The request: “${quote(ctx.turn.text)}”. I will look at the repository before I change anything.`,
  );
  const steps = [
    "Look at the repository",
    "Note the request in NOTES.md",
    "Run the tests",
  ];
  await ctx.plan(
    steps.map((step, index) => [step, index === 0 ? "in_progress" : "pending"]),
  );
  await ctx.tool({
    kind: "execute",
    command: "ls -a /home/workspace",
    summary: "List files in 'workspace'",
    reads: true,
    output: LISTING,
    takes: 500,
  });
  await ctx.tool({
    kind: "read",
    path: "/home/workspace/README.md",
    output: README,
    takes: 400,
  });
  await ctx.plan(
    steps.map((step, index) => [
      step,
      index === 0 ? "completed" : index === 1 ? "in_progress" : "pending",
    ]),
  );
  await ctx.say(
    "The repository is the storefront backend. I will write the request down first, so that it is not lost between turns.",
  );
  const edited = await ctx.tool({
    kind: "edit",
    path: "/home/workspace/NOTES.md",
    asks: true,
    takes: 600,
    before: "## Requests\n",
    after: "## Requests\n" + note + "\n",
    diff: `--- a/NOTES.md\n+++ b/NOTES.md\n@@ -3,3 +3,4 @@\n \n ## Requests\n+${note}\n `,
  });
  if (!edited) return ctx.refused();
  await ctx.plan(
    steps.map((step, index) => [step, index < 2 ? "completed" : "in_progress"]),
  );
  const ran = await ctx.tool({
    kind: "execute",
    command: "npm test",
    asks: true,
    output: TESTS,
    takes: 1800,
  });
  if (!ran) return ctx.refused();
  await ctx.plan(steps.map((step) => [step, "completed"]));
  await ctx.say(
    "Done. Here is what happened in this turn:\n\n" +
      "1. I listed the workspace and read `README.md`.\n" +
      "2. I added the request to `NOTES.md`.\n" +
      "3. I ran `npm test`: **26 tests pass**.\n\n" +
      "The files stay in the session, so the next turn starts from here.",
  );
}

// long keeps working until it is stopped, to try "Stop turn".
async function long(ctx) {
  await ctx.say(
    "This will take a while. I am going through the source tree, one file after another.",
  );
  const files = [
    "cart/price.ts",
    "cart/rules.ts",
    "cart/store.ts",
    "checkout/total.ts",
    "checkout/tax.ts",
    "checkout/session.ts",
    "catalogue/search.ts",
    "catalogue/index.ts",
    "webhooks/sender.ts",
    "webhooks/sign.ts",
    "orders/model.ts",
    "orders/export.ts",
  ];
  for (const [index, file] of files.entries()) {
    await ctx.tool({
      kind: "read",
      path: "/home/workspace/src/" + file,
      output: `// ${file}\n// ${40 + index * 17} lines\n`,
      takes: 1400,
    });
    await ctx.pause(900);
    if (index % 3 === 2)
      await ctx.say(` I have read ${index + 1} of ${files.length} files.`);
  }
  await ctx.say("\n\nThat was every file under `src`.");
}

// seedPush is the turn that the example data leaves running: it has created
// a branch and waits for approval to push it.
async function seedPush(ctx) {
  await ctx.say("I will create the branch and push it.");
  await ctx.tool({
    kind: "execute",
    command: "git checkout -b retry-backoff",
    asks: true,
    output: "Switched to a new branch 'retry-backoff'\n",
    takes: 300,
  });
  const pushed = await ctx.tool({
    kind: "execute",
    command: "git push origin retry-backoff",
    asks: true,
    takes: 1600,
    output:
      "remote: \nremote: Create a pull request for 'retry-backoff' by visiting:\nremote:      https://git.example.com/shop/storefront/pull/new/retry-backoff\nremote: \nTo https://git.example.com/shop/storefront.git\n * [new branch]      retry-backoff -> retry-backoff\n",
  });
  if (!pushed) return ctx.refused();
  await ctx.say(
    "\n\nPushed. The branch `retry-backoff` is on the remote, with the retry and its tests in one commit. You can open a pull request from the link in the output above.",
  );
}

const FAILURES = {
  harness_error: {
    after: 1500,
    message:
      'the model endpoint answered 401 Unauthorized: {"error":{"message":"Incorrect API key provided.","type":"invalid_request_error"}}',
  },
  resume_failed: {
    after: 400,
    message:
      "the harness could not load its saved conversation: session file is not valid JSON",
  },
  startup_timed_out: {
    never: true,
    after: 6000,
    message:
      "the harness did not receive the message within 120 seconds: waiting for the pod of the sandbox to be ready",
  },
  timed_out: {
    after: 5000,
    working: true,
    message: "the turn ran for longer than 1800 seconds",
  },
  sandbox_lost: {
    after: 2500,
    working: true,
    message: "the sandbox of the session is gone",
  },
  session_expired: {
    never: true,
    after: 800,
    ends: true,
    message: "the session was not used for longer than 604800 seconds",
  },
  runner_error: {
    after: 1200,
    message:
      "the runner stopped: connection to the harness closed unexpectedly",
  },
};

function failing(reason) {
  const how = FAILURES[reason];
  const script = async (ctx) => {
    if (how.working) {
      await ctx.say("I will start with the files that the request names.");
      await ctx.tool({
        kind: "execute",
        command: "ls -a /home/workspace",
        summary: "List files in 'workspace'",
        reads: true,
        output: LISTING,
        takes: 500,
      });
      ctx.hang({ kind: "read", path: "/home/workspace/src/checkout/total.ts" });
    }
    await ctx.pause(how.after);
    ctx.fail(reason, how.message, how.ends);
  };
  script.never = how.never;
  return script;
}

// retitle has the harness name its session, as a harness does with the
// session_info_update of the Agent Client Protocol.
async function retitle(ctx) {
  await ctx.pause(400);
  ctx.emit("session.update", { title: "A title that the harness wrote" });
  await answer(ctx);
}

// script returns what a turn plays for a scenario: the name of a script
// here, or "fail:" and a reason. "auto" answers a question and works on
// anything else.
export function script(scenario, text) {
  if (scenario.startsWith("fail:")) return failing(scenario.slice(5));
  const named = { answer, work, long, retitle, seedPush, approval: work }[
    scenario
  ];
  if (named) return named;
  const question = text.trim().length < 28 || /\?\s*$/.test(text.trim());
  return question ? answer : work;
}
