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
//   ctx.fail(reason, message)               ends the turn as failed

const quote = (text) => (text.length > 140 ? text.slice(0, 139).trimEnd() + '…' : text);

async function answer(ctx) {
  await ctx.think('A short question. No tools are needed for this.');
  await ctx.say(
    `You said: “${quote(ctx.turn.text)}”\n\n` +
    'This is the mock of the Aviary console, so no model is behind this agent and the reply is scripted. ' +
    'Everything around it is what the API does: the message became a **turn**, the turn was taken from the queue, ' +
    'its events were streamed to this page, and its record was stored when it ended.\n\n' +
    'To see more, pick a script under **Next turn** in the bar at the top:\n\n' +
    '- *Works with tools*: a plan, tool calls and their output\n' +
    '- *Asks for approval*: the turn waits for a person\n' +
    '- *Queued first*, *Queue is full*, *Expires in the queue*\n' +
    '- *Fails*, for each reason that the API reports');
}

const LISTING = '.\n..\n.git\n.github\nNOTES.md\nREADME.md\npackage.json\nsrc\ntest\ntsconfig.json\n';
const README = '# storefront\n\nThe storefront backend: catalogue, cart, checkout and webhooks.\n\n## Develop\n\n    npm install\n    npm test\n';
const TESTS =
  ' RUN  v3.2.4 /home/workspace\n\n' +
  ' ✓ test/cart/price.test.ts (14 tests) 61ms\n' +
  ' ✓ test/checkout/total.test.ts (9 tests) 48ms\n' +
  ' ✓ test/webhooks/sender.test.ts (3 tests) 1834ms\n\n' +
  ' Test Files  3 passed (3)\n      Tests  26 passed (26)\n   Duration  2.71s\n';

// work looks around, changes a file and runs the tests. If the agent asks
// before it acts, the change and the command each wait for approval.
async function work(ctx) {
  const note = `- ${new Date().toISOString().slice(0, 10)}: ${quote(ctx.turn.text)}`;
  await ctx.think(`The request: “${quote(ctx.turn.text)}”. I will look at the repository before I change anything.`);
  const steps = ['Look at the repository', 'Note the request in NOTES.md', 'Run the tests'];
  await ctx.plan(steps.map((step, index) => [step, index === 0 ? 'in_progress' : 'pending']));
  await ctx.tool({kind: 'execute', command: 'ls -a /home/workspace', summary: "List files in 'workspace'", reads: true, output: LISTING, takes: 500});
  await ctx.tool({kind: 'read', path: '/home/workspace/README.md', output: README, takes: 400});
  await ctx.plan(steps.map((step, index) => [step, index === 0 ? 'completed' : index === 1 ? 'in_progress' : 'pending']));
  await ctx.say('The repository is the storefront backend. I will write the request down first, so that it is not lost between turns.');
  const edited = await ctx.tool({
    kind: 'edit', path: '/home/workspace/NOTES.md', asks: true, takes: 600,
    before: '## Requests\n', after: '## Requests\n' + note + '\n',
    diff: `--- a/NOTES.md\n+++ b/NOTES.md\n@@ -3,3 +3,4 @@\n \n ## Requests\n+${note}\n `,
  });
  if (!edited) return ctx.refused();
  await ctx.plan(steps.map((step, index) => [step, index < 2 ? 'completed' : 'in_progress']));
  const ran = await ctx.tool({kind: 'execute', command: 'npm test', asks: true, output: TESTS, takes: 1800});
  if (!ran) return ctx.refused();
  await ctx.plan(steps.map((step) => [step, 'completed']));
  await ctx.say(
    'Done. Here is what happened in this turn:\n\n' +
    '1. I listed the workspace and read `README.md`.\n' +
    '2. I added the request to `NOTES.md`.\n' +
    '3. I ran `npm test`: **26 tests pass**.\n\n' +
    'The files stay in the session, so the next turn starts from here.');
}

// long keeps working until it is stopped, to try "Stop turn".
async function long(ctx) {
  await ctx.say('This will take a while. I am going through the source tree, one file after another.');
  const files = ['cart/price.ts', 'cart/rules.ts', 'cart/store.ts', 'checkout/total.ts', 'checkout/tax.ts', 'checkout/session.ts',
    'catalogue/search.ts', 'catalogue/index.ts', 'webhooks/sender.ts', 'webhooks/sign.ts', 'orders/model.ts', 'orders/export.ts'];
  for (const [index, file] of files.entries()) {
    await ctx.tool({kind: 'read', path: '/home/workspace/src/' + file, output: `// ${file}\n// ${40 + index * 17} lines\n`, takes: 1400});
    await ctx.pause(900);
    if (index % 3 === 2) await ctx.say(` I have read ${index + 1} of ${files.length} files.`);
  }
  await ctx.say('\n\nThat was every file under `src`.');
}

// seedPush is the turn that the example data leaves running: it has created
// a branch and waits for approval to push it.
async function seedPush(ctx) {
  await ctx.say('I will create the branch and push it.');
  await ctx.tool({kind: 'execute', command: 'git checkout -b retry-backoff', asks: true, output: "Switched to a new branch 'retry-backoff'\n", takes: 300});
  const pushed = await ctx.tool({
    kind: 'execute', command: 'git push origin retry-backoff', asks: true, takes: 1600,
    output: "remote: \nremote: Create a pull request for 'retry-backoff' by visiting:\nremote:      https://git.example.com/shop/storefront/pull/new/retry-backoff\nremote: \nTo https://git.example.com/shop/storefront.git\n * [new branch]      retry-backoff -> retry-backoff\n",
  });
  if (!pushed) return ctx.refused();
  await ctx.say('\n\nPushed. The branch `retry-backoff` is on the remote, with the retry and its tests in one commit. You can open a pull request from the link in the output above.');
}

const FAILURES = {
  harness_error: {after: 1500, message: 'the model endpoint answered 401 Unauthorized: {"error":{"message":"Incorrect API key provided.","type":"invalid_request_error"}}'},
  resume_failed: {after: 400, message: 'the harness could not load its saved conversation: session file is not valid JSON'},
  startup_timed_out: {never: true, after: 6000, message: 'the harness did not receive the message within 120 seconds: waiting for the pod of the sandbox to be ready'},
  timed_out: {after: 5000, working: true, message: 'the turn ran for longer than 1800 seconds'},
  sandbox_lost: {after: 2500, working: true, message: 'the sandbox of the session is gone'},
  session_expired: {never: true, after: 800, ends: true, message: 'the session was not used for longer than 604800 seconds'},
  runner_error: {after: 1200, message: 'the runner stopped: connection to the harness closed unexpectedly'},
};

function failing(reason) {
  const how = FAILURES[reason];
  const script = async (ctx) => {
    if (how.working) {
      await ctx.say('I will start with the files that the request names.');
      await ctx.tool({kind: 'execute', command: 'ls -a /home/workspace', summary: "List files in 'workspace'", reads: true, output: LISTING, takes: 500});
      ctx.hang({kind: 'read', path: '/home/workspace/src/checkout/total.ts'});
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
  ctx.emit('session.update', {title: 'A title that the harness wrote'});
  await answer(ctx);
}

export const SCENARIOS = [
  ['auto', 'Auto'],
  ['answer', 'Plain answer'],
  ['work', 'Works with tools'],
  ['approval', 'Asks for approval'],
  ['long', 'Long run (to stop it)'],
  ['queue', 'Queued first'],
  ['queue_full', 'Queue is full (429)'],
  ['expire', 'Expires in the queue'],
  ['retitle', 'Harness suggests a title'],
  ...Object.keys(FAILURES).map((reason) => ['fail:' + reason, 'Fails: ' + reason]),
];

// script returns what a turn plays for the scenario chosen in the bar of the
// mock. "auto" answers a question and works on anything else.
export function script(scenario, text) {
  if (scenario.startsWith('fail:')) return failing(scenario.slice(5));
  const named = {answer, work, long, retitle, seedPush, approval: work}[scenario];
  if (named) return named;
  const question = text.trim().length < 28 || /\?\s*$/.test(text.trim());
  return question ? answer : work;
}
