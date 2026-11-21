// The Supervisor of the mock. It answers the requests of the console from
// memory, with the paths, status codes, permissions and events of the real
// API (README.md, "API"), so that the console is written against the real
// thing. Nothing here is part of the product: the product's api.js sends the
// same requests over HTTP.

import {uuid7, copy, emitter} from './util.js';
import {seed, PERMISSIONS, IDLE} from './seed.js';
import {script} from './scripts.js';
import {assemble} from './items.js';

// What the mock is set to do, from its bar at the top of the page.
export const mock = {
  // The script of the next turn. It goes back to "auto" once it is used.
  scenario: 'auto',
  // How fast the agents work: 1 is their usual pace, 0 is at once.
  speed: 1,
  changed: emitter(),
};

let db;
let eventsOf;
let watchers;
let approvalsOf;
let runs;
let feeds;
let told;
let telling = 0;
let serial = 0;

const iso = () => new Date().toISOString();
const LAST = new Set(['turn.completed', 'turn.failed', 'turn.cancelled', 'turn.expired']);

// ---- Errors ---------------------------------------------------------------

class Refusal extends Error {
  constructor(status, code, message, headers = {}) {
    super(message);
    this.response = {status, headers, body: {code, message}};
  }
}
const refuse = (status, code, message, headers) => { throw new Refusal(status, code, message, headers); };
const notFound = () => refuse(404, 'not_found', 'no such resource');
const invalid = (message) => refuse(400, 'invalid_request', message);
const lacks = (permission) => refuse(403, 'forbidden', `the caller does not have the ${permission} permission in the project`);

// ---- Callers and permissions ----------------------------------------------

// identify returns the caller of a request. A token of the mock is the key of
// an account of its identity provider.
function identify(token) {
  if (!db.config.idp) return {id: undefined, groups: [], admin: true};
  const account = Object.hasOwn(db.accounts, token) ? db.accounts[token] : null;
  if (!account) refuse(401, 'unauthenticated', "a valid bearer token from this installation's identity provider is required", {'WWW-Authenticate': 'Bearer'});
  let user = db.users.find((other) => other.subject === account.subject);
  if (!user) {
    user = {id: uuid7(), subject: account.subject, username: account.username, display_name: account.display_name, created_at: iso()};
    db.users.push(user);
    // A member that was added by username before the user's first request
    // becomes that user.
    for (const member of db.members) {
      if (member.user && !member.user.id && member.user.username === user.username) member.user = {id: user.id};
    }
  }
  return {
    id: user.id, subject: account.subject, username: account.username, display_name: account.display_name, groups: account.groups,
    admin: account.groups.some((group) => db.config.admins.includes(group)),
  };
}

function allowed(who) {
  if (!who.admin && db.config.users.length && !who.groups.some((group) => db.config.users.includes(group))) {
    refuse(403, 'forbidden', 'the caller is not in any group that may use this installation');
  }
}

function permissionsOf(projectID, who) {
  const set = new Set();
  for (const member of db.members) {
    if (member.project_id !== projectID) continue;
    const theirs = member.user ? member.user.id && member.user.id === who.id : who.groups.includes(member.group);
    if (!theirs) continue;
    const granted = member.role ? db.roles.find((role) => role.name === member.role).permissions : member.permissions;
    for (const permission of granted) set.add(permission);
  }
  return PERMISSIONS.filter((permission) => set.has(permission));
}

// held returns the caller's permissions in the project. A caller that is not
// a member gets 404, as if the project did not exist.
function held(who, projectID) {
  const permissions = permissionsOf(projectID, who);
  if (!permissions.length) notFound();
  return permissions;
}

function may(who, projectID, permission) {
  if (who.admin) return;
  if (!held(who, projectID).includes(permission)) lacks(permission);
}

function administrator(who) {
  if (!who.admin) refuse(403, 'forbidden', 'only installation administrators can do this');
}

// mayWith checks an act on a session, or on its turn that sender sent.
function mayWith(who, session, sender, act) {
  if (who.admin) return;
  const agent = one(db.agents, session.agent_id);
  const permissions = held(who, agent.project_id);
  let needed = 'sessions.run';
  if (act === 'reads' && session.created_by !== who.id) needed = 'sessions.view_all';
  else if (act === 'sends' && session.created_by !== who.id) refuse(403, 'forbidden', "only the session's creator can send messages to it");
  else if (act === 'controls' && sender !== who.id) needed = 'sessions.control_all';
  else if ((act === 'ends' || act === 'retitles') && session.created_by !== who.id) needed = 'sessions.control_all';
  if (!permissions.includes(needed)) lacks(needed);
}

// ---- Helpers --------------------------------------------------------------

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function one(list, id) {
  const found = UUID.test(id) && list.find((record) => record.id === id);
  if (!found) notFound();
  return found;
}

// decode checks a request body as the API does: it must be an object, and a
// field that the endpoint does not know is an error.
function decode(body, known, optional = false) {
  if (body == null) {
    if (optional) return {};
    refuse(400, 'invalid_request', 'request body is required');
  }
  if (typeof body !== 'object' || Array.isArray(body) || Object.keys(body).some((key) => !known.includes(key))) {
    invalid('request body is not valid JSON for this endpoint');
  }
  return body;
}

function text(value, absent = '') {
  if (value == null) return absent;
  if (typeof value !== 'string') invalid('request body is not valid JSON for this endpoint');
  return value;
}

const SLUG = /^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$/;
function checkSlug(slug) {
  if (!slug) invalid('slug is required');
  if (!SLUG.test(slug)) invalid('slug must be 1 to 63 lowercase letters, digits and hyphens, beginning with a letter and not ending with a hyphen');
}

function page(query, name, absent, most) {
  const value = query.get(name);
  if (value == null || value === '') return absent;
  const number = Number(value);
  if (!Number.isInteger(number) || number < 1 || number > most) invalid(`${name} must be a number from 1 to ${most}`);
  return number;
}

// granted validates the asked permissions and returns the set that they
// grant, implied ones included, in their canonical order.
function granted(asked) {
  if (!Array.isArray(asked)) invalid('request body is not valid JSON for this endpoint');
  for (const permission of asked) {
    if (!PERMISSIONS.includes(permission)) invalid(`unknown permission "${permission}"; the permissions are ${PERMISSIONS.join(', ')}`);
  }
  return PERMISSIONS.filter((permission) => permission === 'project.view' || asked.includes(permission) ||
    (permission === 'sessions.view_all' && asked.includes('sessions.control_all')));
}

// ---- Representations ------------------------------------------------------

function memberRecord(member) {
  const record = {id: member.id, project_id: member.project_id};
  if (member.user) {
    const user = member.user.id && db.users.find((other) => other.id === member.user.id);
    record.user = user ? {id: user.id, username: user.username, display_name: user.display_name} : {username: member.user.username};
  } else {
    record.group = member.group;
  }
  record.role = member.role || 'custom';
  if (!member.role) record.permissions = member.permissions;
  Object.assign(record, {created_at: member.created_at, updated_at: member.updated_at, updated_by: member.updated_by});
  return record;
}

function providerRecord(provider) {
  const record = {id: provider.id, project_id: provider.project_id, name: provider.name, url: provider.url, created_at: provider.created_at, updated_at: provider.updated_at};
  if (provider.api_key_at) record.api_key = provider.api_key_at;
  else if (provider.api_key_sealed) record.api_key = {stored: true};
  return record;
}

function storeRecord(store) {
  return {id: store.id, project_id: store.project_id, name: store.name, kind: store.kind, settings: store.settings, credential: {stored: true},
    created_at: store.created_at, updated_at: store.updated_at};
}

// sessionRecord is a session with its newest turn, as far as a list of
// sessions shows a turn: without its text and items.
function sessionRecord(session) {
  const {allowed: _, ...record} = session;
  const newest = db.turns.filter((turn) => turn.session_id === session.id).sort((a, b) => b.seq - a.seq)[0];
  if (newest) {
    const {text: __, items: ___, ...shown} = turnRecord(newest);
    record.last_turn = shown;
  }
  return record;
}

// turnRecord is a turn with its items, which exist once the turn has ended,
// and with the approval requests that wait while it has not.
function turnRecord(turn) {
  const record = {...turn, items: db.items.get(turn.id) || []};
  if (!turn.ended_at) {
    const waiting = [...(approvalsOf.get(turn.id)?.values() || [])].filter((asked) => !asked.resolved).map((asked) => asked.request);
    if (waiting.length) record.approvals = waiting;
  }
  return record;
}

// ---- Model providers and secret stores --------------------------------------

function endpoint(address) {
  let parsed;
  try { parsed = new URL(address); } catch (error) { parsed = null; }
  if (!parsed || !['http:', 'https:'].includes(parsed.protocol) || !parsed.hostname) invalid('url must be a URL that begins with http:// or https://');
  if (parsed.username || parsed.password) invalid("url must not contain a user name or password; set the endpoint's key in api_key");
  if (parsed.search || parsed.hash || address.includes('?') || address.includes('#')) invalid('url must not have a query or fragment');
}

const SEGMENTS = /^[^/]+(\/[^/]+)*$/;
const plainPath = (path) => SEGMENTS.test(path) && !path.split('/').some((segment) => segment === '.' || segment === '..');

function readProvider(body, projectID) {
  const request = decode(body, ['name', 'url', 'api_key']);
  if (!text(request.name)) invalid('name is required');
  endpoint(text(request.url));
  const read = {name: request.name, url: request.url};
  const key = request.api_key;
  if (key == null) return read;
  const shape = `api_key must be {"value": "…"} for a key that Aviary stores, or {"store": "…", "path": "…", "key": "…"} ` +
    'for a key in a secret store of the project; omit it for an endpoint that takes no key';
  if (typeof key !== 'object' || Object.keys(key).some((field) => !['value', 'store', 'path', 'key'].includes(field))) invalid('request body is not valid JSON for this endpoint');
  if (key.value && !key.store && !key.path && !key.key) {
    read.api_key_sealed = true;
  } else if (!key.value && key.store && key.path && key.key) {
    const store = UUID.test(key.store) && db.stores.find((other) => other.id === key.store && other.project_id === projectID);
    if (!store) invalid('api_key.store must be the ID of a secret store of the project');
    // The key is read once, so that a key that cannot be read is reported now
    // and not during a turn. The store of the mock has no secret below
    // "missing", and no key named "none".
    const cannot = (why) => invalid('cannot read api_key from the secret store: ' + why);
    if (!plainPath(key.path)) cannot('path must be a secret path below the mount, such as team/model, without empty or dot segments');
    if (key.path.split('/').includes('missing')) cannot('secret not found');
    if (key.key === 'none') cannot('secret has no such key');
    read.api_key_at = {store: key.store, path: key.path, key: key.key};
  } else {
    invalid(shape);
  }
  return read;
}

function readStore(body) {
  const request = decode(body, ['name', 'kind', 'settings', 'credential']);
  if (!text(request.name)) invalid('name is required');
  if (!request.credential || !request.credential.value) invalid("credential.value is required: the store's login credential");
  if (!db.config.secretStoreKinds.includes(request.kind)) invalid('kind must be one of: ' + db.config.secretStoreKinds.join(', '));
  const settings = request.settings;
  const wrong = (why) => invalid('settings: ' + why);
  if (!settings || typeof settings !== 'object' || Array.isArray(settings) || Object.keys(settings).some((key) => !['address', 'mount', 'role_id'].includes(key))) {
    wrong('expected an object with address, mount and role_id');
  }
  let address;
  try { address = new URL(settings.address); } catch (error) { address = null; }
  if (!address || !['http:', 'https:'].includes(address.protocol) || !address.hostname) wrong('address must be an http:// or https:// URL');
  if (address.username || address.password) wrong('address must not contain a user name or password');
  if ((address.pathname && address.pathname !== '/') || address.search || address.hash) wrong('address must not have a path, query or fragment');
  if (!settings.role_id) wrong('role_id is required');
  if (!settings.mount || !plainPath(settings.mount)) wrong('mount is required: a path such as secret, without empty or dot segments');
  const port = address.port || (address.protocol === 'https:' ? '443' : '80');
  const hosts = db.config.secretStoreHosts;
  const listed = hosts.includes('*') || hosts.includes(`${address.hostname}:${port}`) || (hosts.includes(address.hostname) && ['443', '80'].includes(port));
  if (!listed) wrong('host is not in supervisor.secretStores.allowedHosts');
  // The Supervisor logs in to the store before it accepts it. The store of
  // the mock refuses the credential "wrong".
  if (request.credential.value === 'wrong') invalid('login failed: store returned status 400');
  return {name: request.name, kind: request.kind, settings: copy(settings), credential: 'sealed'};
}

// ---- Turns ----------------------------------------------------------------

function emit(turnID, type, data) {
  if (!eventsOf.has(turnID)) eventsOf.set(turnID, []);
  eventsOf.get(turnID).push({id: `${Date.now()}-${serial++}`, type, data: JSON.stringify(data)});
  for (const watcher of [...(watchers.get(turnID) || [])]) queueMicrotask(watcher);
  changed();
}

// ---- Changes --------------------------------------------------------------

// changed tells the streams of changes about every session that no longer
// is as they were told, with the session as it is now. Changes that come
// together are told once.
function changed() {
  if (telling || !told) return;
  telling = setTimeout(() => {
    telling = 0;
    for (const session of db.sessions) {
      const data = JSON.stringify(sessionRecord(session));
      if (told.get(session.id) === data) continue;
      told.set(session.id, data);
      const project = db.agents.find((agent) => agent.id === session.agent_id).project_id;
      for (const feed of [...feeds]) {
        if (feed.project === project && feed.sees(session)) feed.onEvent({type: 'session', data});
      }
    }
  }, 40);
}

// last returns the event that reports how an ended turn ended.
function last(turn) {
  switch (turn.state) {
    case 'completed': return ['turn.completed', {stop_reason: turn.stop_reason, ...(turn.usage ? {usage: turn.usage} : {})}];
    case 'failed': return ['turn.failed', turn.failure];
    case 'cancelled': return ['turn.cancelled', {}];
    default: return ['turn.expired', {}];
  }
}

function end(turn, state, more = {}) {
  Object.assign(turn, {state, ended_at: iso()}, more);
  db.items.set(turn.id, assemble(eventsOf.get(turn.id) || []));
  runs.delete(turn.id);
  emit(turn.id, ...last(turn));
}

class Cancelled extends Error {}
class Failed extends Error {
  constructor(reason, message, endsSession) {
    super(message);
    this.failure = {reason, message};
    this.endsSession = endsSession;
  }
}

const OPTIONS = {
  opencode: () => [
    {id: 'once', kind: 'allow_once', name: 'Allow once'},
    {id: 'always', kind: 'allow_always', name: 'Always allow'},
    {id: 'reject', kind: 'reject_once', name: 'Reject'},
  ],
  codex: (call) => [
    {id: 'allow_once', kind: 'allow_once', name: 'Yes, proceed'},
    {id: 'accept_execpolicy_amendment', kind: 'allow_always', name: `Yes, and don't ask again for commands that start with \`${call.input?.command || call.title}\``},
    {id: 'cancel', kind: 'reject_once', name: 'No, and tell Codex what to do differently'},
  ],
};

const callID = () => 'call_' + Array.from({length: 26}, () => 'abcdefghijklmnopqrstuvwxyz234567'[Math.floor(Math.random() * 32)]).join('');
const relative = (path) => path.replace(/^\/home\/workspace\//, '');
const base = (path) => path.split('/').pop();

// run plays a script as the turn. answers, by request ID, are the approvals
// that a person has already given: the turn does not wait for those.
function run(turn, play, {answers = {}, instant = false} = {}) {
  const session = db.sessions.find((other) => other.id === turn.session_id);
  const agent = db.agents.find((other) => other.id === session.agent_id);
  const harness = agent.harness === 'codex' ? 'codex' : 'opencode';
  const state = {cancelled: false, instant, wake: null, asked: 0, written: 0, stop: 'end_turn'};
  runs.set(turn.id, state);

  const check = () => { if (state.cancelled) throw new Cancelled(); };
  const pause = (ms) => {
    check();
    if (state.instant || mock.speed === 0) return Promise.resolve().then(check);
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => { state.wake = null; resolve(); }, ms * mock.speed);
      state.wake = () => { clearTimeout(timer); reject(new Cancelled()); };
    });
  };
  const send = (type, data) => { check(); emit(turn.id, type, data); };

  // write streams text as deltas, a few characters at a time.
  const write = async (type, words) => {
    if (state.instant || mock.speed === 0) {
      send(type, {text: words});
      return;
    }
    const pieces = words.match(/\s*\S{1,6}|\s+/g) || [];
    for (let i = 0; i < pieces.length; i += 2) {
      send(type, {text: pieces.slice(i, i + 2).join('')});
      await pause(26);
    }
  };

  // ask sends an approval request and waits for its answer.
  const ask = async (call) => {
    const id = String(++state.asked);
    const request = {id, tool_call: call, options: OPTIONS[harness](call)};
    const asked = {request, answered: false, resolved: false};
    if (!approvalsOf.has(turn.id)) approvalsOf.set(turn.id, new Map());
    approvalsOf.get(turn.id).set(id, asked);
    send('approval.requested', request);
    if (answers[id]) {
      Object.assign(asked, {answered: true, optionID: answers[id].option, by: answers[id].by});
    } else {
      state.instant = false;
      await new Promise((resolve, reject) => {
        asked.settle = resolve;
        state.wake = () => reject(new Cancelled());
      });
      state.wake = null;
      await pause(180);
    }
    asked.resolved = true;
    const resolved = {id, outcome: 'selected', option_id: asked.optionID};
    if (asked.by) resolved.answered_by = asked.by;
    send('approval.resolved', resolved);
    return request.options.find((option) => option.id === asked.optionID);
  };

  // needsApproval reports whether the harness asks before this tool call.
  // OpenCode asks before a command or a change to a file, Codex before a
  // command, and neither asks again after "always".
  const needsApproval = (spec) => {
    if (!spec.asks || agent.approval !== 'ask') return false;
    if (harness === 'codex' && spec.kind !== 'execute') return false;
    return !(session.allowed || []).includes(spec.kind);
  };
  const approve = async (spec, call) => {
    if (!needsApproval(spec)) return true;
    const chosen = await ask(call);
    if (chosen.kind === 'allow_always') session.allowed = [...(session.allowed || []), spec.kind];
    return chosen.kind.startsWith('allow');
  };

  const opencodeTool = async (spec) => {
    const id = callID();
    const name = {execute: 'bash', read: 'read', edit: 'edit', search: 'grep'}[spec.kind] || spec.kind;
    const title = spec.command || relative(spec.path);
    const input = spec.command ? {command: spec.command, cwd: '/home/workspace'}
      : spec.kind === 'edit' ? {filePath: spec.path, oldString: spec.before, newString: spec.after}
        : {filePath: spec.path};
    send('tool.call', {id, title: name, kind: spec.kind, status: 'pending', input: {}});
    await pause(120);
    send('tool.update', {id, title, kind: spec.kind, status: 'pending', input});
    if (!await approve(spec, {id, title, kind: spec.kind, status: 'pending', input})) {
      send('tool.update', {id, status: 'failed', output: {error: 'The user rejected permission to use this specific tool call.'}});
      return false;
    }
    send('tool.update', {id, title, kind: spec.kind, status: 'in_progress', input});
    await pause(spec.takes || 600);
    const output = spec.kind === 'edit' ? {output: 'Edit applied successfully.', metadata: {diff: spec.diff}}
      : spec.command ? {output: spec.output, metadata: {output: spec.output, exit: 0, truncated: false}}
        : {output: spec.output, metadata: {truncated: false}};
    send('tool.update', {id, status: 'completed', title, output});
    return true;
  };

  // Codex runs commands in a terminal of its own, and reports their output
  // only in the meta of the events: its tool calls end with an exit code.
  const codexTool = async (spec) => {
    const id = callID();
    const command = spec.command || (spec.kind === 'read' ? `sed -n 1,200p ${relative(spec.path)}` : null);
    if (spec.kind === 'edit') {
      const title = 'Edit ' + base(spec.path);
      send('tool.call', {id, title, kind: 'edit', status: 'in_progress', input: {changes: {[spec.path]: {type: 'update', unified_diff: spec.diff}}}});
      await pause(spec.takes || 600);
      send('tool.update', {id, status: 'completed'});
      return true;
    }
    const kind = spec.reads ? 'read' : spec.kind;
    const title = spec.summary || (spec.kind === 'read' ? 'Read ' + base(spec.path) : command);
    const input = {command, cwd: '/home/workspace'};
    if (!await approve(spec, {id, title, kind, status: 'pending', input})) {
      send('tool.call', {id, title, kind, status: 'failed', input, meta: {terminal_exit: {exit_code: null, signal: null, terminal_id: id}}});
      return false;
    }
    send('tool.call', {id, title, kind, status: 'in_progress', input, meta: {terminal_info: {cwd: '/home/workspace', terminal_id: id}}});
    await pause(spec.takes || 600);
    send('tool.update', {id, status: 'completed', output: {exit_code: 0}, meta: {terminal_output_delta: {data: spec.output || '', terminal_id: id}}});
    send('tool.update', {id, meta: {terminal_exit: {exit_code: 0, signal: null, terminal_id: id}}});
    return true;
  };

  const context = {
    turn, session, agent, harness, pause,
    emit: send,
    think: (words) => write('thought.delta', words),
    say: async (words) => { state.written += words.length; await write('message.delta', words); },
    plan: async (entries) => {
      send('plan.update', {entries: entries.map(([content, status]) => ({content, priority: 'medium', status}))});
      await pause(200);
    },
    tool: (spec) => (harness === 'codex' ? codexTool(spec) : opencodeTool(spec)),
    // hang starts a tool call and leaves it unfinished.
    hang: (spec) => {
      const title = spec.command || relative(spec.path);
      send('tool.call', {id: callID(), title, kind: spec.kind, status: 'in_progress', input: spec.command ? {command: spec.command} : {filePath: spec.path}});
    },
    // refused ends the turn after a person refused an action. Both harnesses
    // end the turn, Codex with the stop reason "cancelled".
    refused: () => { state.stop = harness === 'codex' ? 'cancelled' : 'end_turn'; },
    fail: (reason, message, endsSession) => { throw new Failed(reason, message, endsSession); },
  };

  const usage = () => {
    const input = 5200 + turn.seq * 2600 + Math.floor(Math.random() * 900);
    const output = 40 + Math.ceil(state.written / 3.6);
    const reported = {total_tokens: input + output, input_tokens: input, output_tokens: output};
    return harness === 'codex' ? {...reported, thought_tokens: 0, cached_read_tokens: Math.floor(input * 0.6), cached_write_tokens: 0} : reported;
  };

  (async () => {
    try {
      if (!turn.started_at && !play.never) {
        // The first turn of a session waits for its sandbox to be created, as
        // does the turn after the sandbox was lost. A later one waits for it
        // to wake if it sleeps. The session says that its sandbox is awake
        // from the moment that it is created or woken.
        const wait = !session.sandbox || session.sandbox === 'lost' ? 1900 : session.sandbox === 'asleep' ? 1500 : 350;
        session.sandbox = 'awake';
        changed();
        await pause(wait);
        turn.started_at = iso();
        send('turn.started', {});
        await pause(300);
      }
      await play(context);
      check();
      end(turn, 'completed', {stop_reason: state.stop, usage: usage()});
    } catch (error) {
      if (error instanceof Cancelled) {
        end(turn, 'cancelled', turn.started_at ? {stop_reason: 'cancelled'} : {});
      } else if (error instanceof Failed) {
        // A turn that ran for too long leaves its sandbox asleep, and the
        // session says of a sandbox that is gone that it is lost. A session
        // that expired has ended.
        if (error.failure.reason === 'timed_out') session.sandbox = 'asleep';
        if (error.failure.reason === 'sandbox_lost') session.sandbox = 'lost';
        if (error.endsSession) {
          Object.assign(session, {ended_at: iso(), end_reason: 'expired'});
          delete session.sandbox;
        }
        end(turn, 'failed', {failure: error.failure});
      } else {
        console.error('mock: script failed', error);
        end(turn, 'failed', {failure: {reason: 'runner_error', message: String(error)}});
      }
    }
  })();
}

// lull puts to sleep the sandboxes of the sessions that have run no turn for
// a while, as the Supervisor does from time to time.
function lull() {
  for (const session of db.sessions) {
    if (session.sandbox !== 'awake') continue;
    const turns = db.turns.filter((turn) => turn.session_id === session.id);
    if (turns.some((turn) => !turn.ended_at)) continue;
    const last = Math.max(...turns.map((turn) => Date.parse(turn.ended_at)));
    if (Date.now() - last >= IDLE * mock.speed) session.sandbox = 'asleep';
  }
  changed();
}

// cancel stops what a turn is doing. The requests that still wait for an
// answer end as cancelled.
function cancel(turn) {
  const state = runs.get(turn.id);
  for (const asked of approvalsOf.get(turn.id)?.values() || []) {
    if (!asked.resolved) {
      asked.resolved = true;
      emit(turn.id, 'approval.resolved', {id: asked.request.id, outcome: 'cancelled'});
    }
  }
  if (!state) return;
  state.cancelled = true;
  state.wake?.();
}

// chosen returns the scenario that the bar of the mock is set to, and sets
// the bar back to "auto".
function chosen() {
  const scenario = mock.scenario;
  if (scenario !== 'auto') {
    mock.scenario = 'auto';
    mock.changed.emit();
  }
  return scenario;
}

// accept makes a message the next turn of its session, and starts it as the
// scenario says.
function accept(session, who, words, scenario) {
  if (session.ended_at) refuse(409, 'session_ended', 'the session has ended');
  const turns = db.turns.filter((turn) => turn.session_id === session.id);
  if (turns.some((turn) => !turn.ended_at)) refuse(409, 'session_busy', 'the session has an unfinished turn');
  if (scenario === 'queue_full') refuse(429, 'queue_full', 'too many turns are queued; retry later', {'Retry-After': '5'});
  const turn = {id: uuid7(), session_id: session.id, seq: turns.length + 1, state: 'queued', text: words, created_by: who.id, created_at: iso()};
  db.turns.push(turn);
  db.items.set(turn.id, []);
  const play = script(scenario, words);
  const dispatch = () => { turn.state = 'running'; changed(); run(turn, play); };

  if (scenario !== 'queue' && scenario !== 'expire') {
    queueMicrotask(dispatch);
    return turn;
  }
  // The turn waits in the queue: behind two others, which then run.
  const waiting = {cancelled: false, wake: null};
  runs.set(turn.id, waiting);
  const wait = (ms) => new Promise((resolve, reject) => {
    if (waiting.cancelled) { reject(new Cancelled()); return; }
    const timer = setTimeout(resolve, ms * (mock.speed || 0.02));
    waiting.wake = () => { clearTimeout(timer); reject(new Cancelled()); };
  });
  (async () => {
    try {
      emit(turn.id, 'turn.queued', {position: 3});
      await wait(2200);
      emit(turn.id, 'turn.queued', {position: 2});
      await wait(2600);
      if (scenario === 'expire') {
        await wait(3200);
        end(turn, 'expired');
        return;
      }
      emit(turn.id, 'turn.queued', {position: 1});
      await wait(2200);
      runs.delete(turn.id);
      dispatch();
    } catch (error) {
      if (!(error instanceof Cancelled)) throw error;
    }
  })();
  return turn;
}

// ---- Routes ---------------------------------------------------------------

const routes = [];
const route = (method, path, handler, flag) => {
  const pattern = new RegExp('^' + path.replace(/\{\w+\}/g, '([^/]+)') + '$');
  routes.push({method, path, pattern, handler, flag});
};

route('GET', '/v1/me', ({who}) => {
  const me = {admin: who.admin};
  if (who.id) Object.assign(me, {id: who.id, subject: who.subject, username: who.username, display_name: who.display_name});
  if (who.groups.length) me.groups = who.groups;
  return [200, me];
});

route('GET', '/v1/roles', () => [200, {roles: db.roles}]);
route('PUT', '/v1/roles/{name}', ({who, body}, name) => {
  administrator(who);
  const request = decode(body, ['permissions']);
  if (request.permissions == null) invalid('permissions is required');
  const set = granted(request.permissions);
  const role = db.roles.find((other) => other.name === name);
  if (!role) notFound();
  Object.assign(role, {permissions: set, updated_at: iso()});
  if (who.id) role.updated_by = who.id;
  return [200, role];
});

route('POST', '/v1/projects', ({who, body}) => {
  administrator(who);
  const request = decode(body, ['slug', 'name']);
  checkSlug(text(request.slug));
  if (db.projects.some((project) => project.slug === request.slug)) refuse(409, 'slug_taken', 'the slug is already taken');
  const project = {id: uuid7(), slug: request.slug, name: text(request.name), created_at: iso()};
  db.projects.push(project);
  return [201, project];
});
route('GET', '/v1/projects', ({who}) => {
  const projects = who.admin ? db.projects : db.projects.filter((project) => permissionsOf(project.id, who).length);
  return [200, {projects: [...projects].sort((a, b) => a.created_at.localeCompare(b.created_at))}];
});
route('GET', '/v1/projects/{pid}', ({who}, pid) => {
  const project = inProject(who, pid, 'project.view');
  return [200, project];
});

// inProject returns the project if the caller has the permission in it.
function inProject(who, pid, permission) {
  if (!UUID.test(pid)) notFound();
  may(who, pid, permission);
  return one(db.projects, pid);
}

route('GET', '/v1/projects/{pid}/members', ({who}, pid) => {
  inProject(who, pid, 'project.view');
  return [200, {members: db.members.filter((member) => member.project_id === pid).map(memberRecord)}];
});

// grant reads the role, or the permissions, that a request gives a member.
function grant(request) {
  const role = text(request.role);
  if (!role || role === 'custom') {
    if (request.permissions == null) invalid('a member needs a role or its own permissions');
    return {permissions: granted(request.permissions)};
  }
  if (request.permissions != null) invalid('a member with a role cannot have its own permissions');
  const names = db.roles.map((other) => other.name);
  if (!names.includes(role)) invalid(`unknown role "${role}"; the roles are ${names.join(', ')}`);
  return {role};
}

route('POST', '/v1/projects/{pid}/members', ({who, body}, pid) => {
  administrator(who);
  one(db.projects, pid);
  const request = decode(body, ['user', 'group', 'role', 'permissions']);
  const username = text(request.user), group = text(request.group);
  if (!username === !group) invalid('exactly one of user and group is required');
  const given = grant(request);
  const member = {id: uuid7(), project_id: pid, ...given, created_at: iso(), updated_at: iso()};
  if (who.id) member.updated_by = who.id;
  const mine = db.members.filter((other) => other.project_id === pid);
  if (group) {
    if (mine.some((other) => other.group === group)) refuse(409, 'member_exists', 'the project already has this member');
    member.group = group;
  } else {
    const user = db.users.find((other) => other.username === username);
    const same = (other) => other.user && (user ? other.user.id === user.id : other.user.username === username);
    if (mine.some(same)) refuse(409, 'member_exists', 'the project already has this member');
    member.user = user ? {id: user.id} : {username};
  }
  db.members.push(member);
  return [201, memberRecord(member)];
});
route('PUT', '/v1/members/{mid}', ({who, body}, mid) => {
  administrator(who);
  const given = grant(decode(body, ['role', 'permissions']));
  const member = one(db.members, mid);
  delete member.role;
  delete member.permissions;
  Object.assign(member, given, {updated_at: iso()});
  if (who.id) member.updated_by = who.id;
  return [200, memberRecord(member)];
});
route('DELETE', '/v1/members/{mid}', ({who}, mid) => {
  administrator(who);
  const member = one(db.members, mid);
  db.members.splice(db.members.indexOf(member), 1);
  return [204, null];
});

route('POST', '/v1/projects/{pid}/secret-stores', ({who, body}, pid) => {
  inProject(who, pid, 'connections.manage');
  const store = {id: uuid7(), project_id: pid, ...readStore(body), created_at: iso(), updated_at: iso()};
  db.stores.push(store);
  return [201, storeRecord(store)];
});
route('GET', '/v1/secret-stores/{id}', ({who}, id) => {
  const store = one(db.stores, id);
  may(who, store.project_id, 'project.view');
  return [200, storeRecord(store)];
});
route('PUT', '/v1/secret-stores/{id}', ({who, body}, id) => {
  const store = one(db.stores, id);
  may(who, store.project_id, 'connections.manage');
  Object.assign(store, readStore(body), {updated_at: iso()});
  return [200, storeRecord(store)];
});

route('POST', '/v1/projects/{pid}/providers', ({who, body}, pid) => {
  inProject(who, pid, 'connections.manage');
  const provider = {id: uuid7(), project_id: pid, ...readProvider(body, pid), created_at: iso(), updated_at: iso()};
  db.providers.push(provider);
  return [201, providerRecord(provider)];
});
route('GET', '/v1/providers/{id}', ({who}, id) => {
  const provider = one(db.providers, id);
  may(who, provider.project_id, 'project.view');
  return [200, providerRecord(provider)];
});
route('PUT', '/v1/providers/{id}', ({who, body}, id) => {
  const provider = one(db.providers, id);
  may(who, provider.project_id, 'connections.manage');
  const read = readProvider(body, provider.project_id);
  // The name, the URL and the key are replaced together: a provider that is
  // given no key has none afterwards.
  delete provider.api_key_sealed;
  delete provider.api_key_at;
  Object.assign(provider, read, {updated_at: iso()});
  return [200, providerRecord(provider)];
});

route('POST', '/v1/projects/{pid}/agents', ({who, body}, pid) => {
  inProject(who, pid, 'agents.manage');
  const request = decode(body, ['slug', 'name', 'description', 'harness', 'instructions', 'approval', 'model']);
  checkSlug(text(request.slug));
  if (!db.config.harnesses.includes(request.harness)) invalid('harness must be one of the configured harnesses');
  const approval = text(request.approval);
  if (approval && approval !== 'ask') invalid('approval must be "ask" or empty');
  if (request.model == null) invalid('model is required');
  decode(request.model, ['provider', 'name']);
  const provider = UUID.test(request.model.provider) && db.providers.find((other) => other.id === request.model.provider && other.project_id === pid);
  if (!provider) invalid('model.provider must be the ID of a provider of the project');
  if (!text(request.model.name)) invalid('model.name is required');
  if (db.agents.some((agent) => agent.project_id === pid && agent.slug === request.slug)) refuse(409, 'slug_taken', 'the slug is already taken');
  const agent = {id: uuid7(), project_id: pid, slug: request.slug, name: text(request.name), description: text(request.description), harness: request.harness, created_at: iso()};
  if (text(request.instructions)) agent.instructions = request.instructions;
  if (approval) agent.approval = approval;
  agent.model = {provider: provider.id, name: request.model.name};
  db.agents.push(agent);
  return [201, agent];
});
route('GET', '/v1/projects/{pid}/agents', ({who}, pid) => {
  inProject(who, pid, 'project.view');
  return [200, {agents: db.agents.filter((agent) => agent.project_id === pid)}];
});
route('GET', '/v1/agents/{aid}', ({who}, aid) => {
  const agent = one(db.agents, aid);
  may(who, agent.project_id, 'project.view');
  return [200, agent];
});

route('POST', '/v1/agents/{aid}/sessions', ({who, body}, aid) => {
  const agent = one(db.agents, aid);
  may(who, agent.project_id, 'sessions.run');
  const request = decode(body, ['title'], true);
  const session = {id: uuid7(), agent_id: aid, title: text(request.title), created_at: iso()};
  if (who.id) session.created_by = who.id;
  db.sessions.push(session);
  return [201, sessionRecord(session)];
});
// The sessions that the caller may read: its own with sessions.run, and
// everyone's with sessions.view_all or as an administrator.
route('GET', '/v1/agents/{aid}/sessions', ({who, query}, aid) => {
  if (!UUID.test(aid)) notFound();
  const limit = page(query, 'limit', 20, 100);
  const before = query.get('before') || 'ffffffff-ffff-ffff-ffff-ffffffffffff';
  if (!UUID.test(before)) invalid('before must be an ID');
  const agent = one(db.agents, aid);
  let by = null, readable = true;
  if (!who.admin) {
    const permissions = held(who, agent.project_id);
    if (!permissions.includes('sessions.view_all')) {
      by = who.id;
      readable = permissions.includes('sessions.run');
    }
  }
  const sessions = !readable ? [] : db.sessions
    .filter((session) => session.agent_id === aid && (!by || session.created_by === by) && session.id < before)
    .sort((a, b) => (a.id < b.id ? 1 : -1)).slice(0, limit).map(sessionRecord);
  return [200, {sessions}];
});

// withSession returns the session if the caller may do the act on it.
function withSession(who, sid, act) {
  const session = one(db.sessions, sid);
  mayWith(who, session, null, act);
  return session;
}
function withTurn(who, tid, act) {
  const turn = one(db.turns, tid);
  mayWith(who, one(db.sessions, turn.session_id), turn.created_by, act);
  return turn;
}

route('GET', '/v1/sessions/{sid}', ({who}, sid) => [200, sessionRecord(withSession(who, sid, 'reads'))]);
route('PATCH', '/v1/sessions/{sid}', ({who, body}, sid) => {
  const session = withSession(who, sid, 'retitles');
  const request = decode(body, ['title']);
  if (request.title == null) invalid('title is required');
  session.title = text(request.title);
  return [200, sessionRecord(session)];
});
route('DELETE', '/v1/sessions/{sid}', ({who}, sid) => {
  const session = withSession(who, sid, 'ends');
  if (db.turns.some((turn) => turn.session_id === sid && !turn.ended_at)) refuse(409, 'session_busy', 'the session has an unfinished turn');
  if (!session.ended_at) session.ended_at = iso();
  // The sandbox is deleted with the session.
  delete session.sandbox;
  return [200, sessionRecord(session)];
});
route('POST', '/v1/sessions/{sid}/messages', ({who, body}, sid) => {
  const session = withSession(who, sid, 'sends');
  const request = decode(body, ['text']);
  if (!text(request.text)) invalid('text is required');
  return [202, accept(session, who, request.text, chosen())];
});
route('GET', '/v1/sessions/{sid}/turns', ({who, query}, sid) => {
  withSession(who, sid, 'reads');
  const limit = page(query, 'limit', 20, 100);
  const before = page(query, 'before', 2147483647, 2147483647);
  const turns = db.turns.filter((turn) => turn.session_id === sid && turn.seq < before).sort((a, b) => b.seq - a.seq).slice(0, limit);
  return [200, {turns: turns.map(turnRecord)}];
});

route('GET', '/v1/turns/{tid}', ({who}, tid) => [200, turnRecord(withTurn(who, tid, 'reads'))]);
route('POST', '/v1/turns/{tid}/cancel', ({who}, tid) => {
  const turn = withTurn(who, tid, 'controls');
  if (turn.ended_at) {
    if (turn.state !== 'cancelled') refuse(409, 'turn_ended', 'the turn has ended');
    return [200, turn];
  }
  if (turn.state === 'queued') {
    // A queued turn ends at once.
    cancel(turn);
    end(turn, 'cancelled');
    return [200, turn];
  }
  // A running turn is told to stop, and ends when its harness has stopped.
  if (!turn.cancel_requested_at) {
    turn.cancel_requested_at = iso();
    setTimeout(() => cancel(turn), 500 * mock.speed);
  }
  return [202, turn];
});
route('POST', '/v1/turns/{tid}/approvals/{rid}', ({who, body}, tid, rid) => {
  const turn = withTurn(who, tid, 'controls');
  const chosen = decode(body, ['option_id']);
  const asked = approvalsOf.get(tid)?.get(rid);
  if (!asked) notFound();
  if (!asked.request.options.some((option) => option.id === chosen.option_id)) invalid("option_id must be the ID of one of the request's options");
  if (turn.ended_at) refuse(409, 'turn_ended', 'the turn has ended');
  if (asked.answered || asked.resolved) refuse(409, 'approval_answered', 'the approval request already has an answer');
  Object.assign(asked, {answered: true, optionID: chosen.option_id, by: who.id});
  asked.settle?.();
  return [202, {id: rid, option_id: chosen.option_id}];
});
// The events of a turn are a stream: see open().
route('GET', '/v1/turns/{tid}/events', ({who}, tid) => [200, withTurn(who, tid, 'reads')]);
// The changes of a project are a stream too: see watch().
route('GET', '/v1/projects/{pid}/events', ({who}, pid) => [200, inProject(who, pid, 'project.view')], 'proposed');

route('GET', '/v1/projects/{pid}/providers', ({who}, pid) => {
  inProject(who, pid, 'project.view');
  return [200, {providers: db.providers.filter((provider) => provider.project_id === pid).map(providerRecord)}];
});
route('GET', '/v1/projects/{pid}/secret-stores', ({who}, pid) => {
  inProject(who, pid, 'project.view');
  return [200, {secret_stores: db.stores.filter((store) => store.project_id === pid).map(storeRecord)}];
});
route('GET', '/v1/harnesses', () => [200, {harnesses: [...db.config.harnesses].sort().map((name) => ({name}))}]);

// Fields that the mock adds to what the API returns today, as [pattern of
// the path, "field name"].
const ASSUMED = [
  [/^\/v1\/(agents\/[^/]+\/sessions|sessions\/[^/]+)$/, 'field last_turn'],
];

// flagOf returns "proposed" for a request that the API does not have yet,
// and "field x" for one whose answer has a field that the API does not have
// yet.
export function flagOf(method, path) {
  const [plain] = path.split('?');
  const found = routes.find((candidate) => candidate.method === method && candidate.pattern.test(plain));
  return found?.flag || ASSUMED.find(([pattern]) => pattern.test(plain))?.[1] || '';
}

// request answers one request of the API: {status, headers, body}.
export function request(method, path, {token = '', body = null} = {}) {
  const [plain, search = ''] = path.split('?');
  try {
    const who = identify(token);
    const found = routes.map((candidate) => ({candidate, match: candidate.method === method && plain.match(candidate.pattern)})).find((each) => each.match);
    if (plain !== '/v1/me') allowed(who);
    if (!found) notFound();
    const [status, answer] = found.candidate.handler({who, body, query: new URLSearchParams(search)}, ...found.match.slice(1).map(decodeURIComponent));
    if (method !== 'GET') changed();
    return {status, headers: {}, body: answer == null ? null : copy(answer)};
  } catch (error) {
    if (error instanceof Refusal) return copy(error.response);
    console.error('mock: request failed', method, path, error);
    return {status: 500, headers: {}, body: {code: 'internal', message: 'internal error'}};
  }
}

// open answers GET /v1/turns/{id}/events. If the request is refused, it
// returns the response. Otherwise it calls onEvent with each event after the
// one named by after, as they happen, and onEnd after the last event of the
// turn. The returned close() ends the stream, as a client that goes away.
export function open(turnID, {token = '', after = ''} = {}, onEvent, onEnd) {
  const response = request('GET', `/v1/turns/${turnID}/events`, {token});
  if (response.status !== 200) return {response};
  const events = () => eventsOf.get(turnID) || [];
  let next = 0;
  if (after) {
    const at = events().findIndex((event) => event.id === after);
    if (!/^\d+-\d+$/.test(after)) return {response: {status: 400, headers: {}, body: {code: 'invalid_request', message: 'Last-Event-ID is not an event ID'}}};
    next = at + 1;
  }
  let closed = false;
  const close = () => {
    closed = true;
    watchers.get(turnID)?.delete(pump);
  };
  function pump() {
    if (closed) return;
    const list = events();
    while (next < list.length) {
      const event = list[next++];
      onEvent({...event});
      if (closed) return;
      if (LAST.has(event.type)) { close(); onEnd(); return; }
    }
    // The last event of an ended turn is missing if its events are gone. It
    // is then made from the record of the turn.
    const turn = db.turns.find((other) => other.id === turnID);
    if (turn.ended_at) {
      const [type, data] = last(turn);
      onEvent({id: '', type, data: JSON.stringify(data)});
      close();
      onEnd();
    }
  }
  if (!watchers.has(turnID)) watchers.set(turnID, new Set());
  watchers.get(turnID).add(pump);
  queueMicrotask(pump);
  return {response: {status: 200, headers: {'Content-Type': 'text/event-stream'}, body: null}, close};
}

// watch answers GET /v1/projects/{id}/events. If the request is refused, it
// returns the response. Otherwise it calls onEvent with a "session" event for
// every session of the project that changes from then on and that the caller
// may read: the session as it is after the change. The returned close() ends
// the stream.
export function watch(projectID, {token = ''} = {}, onEvent) {
  const response = request('GET', `/v1/projects/${projectID}/events`, {token});
  if (response.status !== 200) return {response};
  const who = identify(token);
  const held = who.admin ? PERMISSIONS : permissionsOf(projectID, who);
  const all = held.includes('sessions.view_all');
  const own = held.includes('sessions.run');
  const feed = {project: projectID, sees: (session) => all || (own && session.created_by === who.id), onEvent};
  feeds.add(feed);
  return {response: {status: 200, headers: {'Content-Type': 'text/event-stream'}, body: null}, close: () => feeds.delete(feed)};
}

// ---- The controls of the mock ---------------------------------------------

export function reset() {
  db = seed(Date.now());
  eventsOf = new Map();
  watchers = new Map();
  approvalsOf = new Map();
  runs = new Map();
  feeds = new Set();
  told = null;
  for (const left of db.live) {
    const turn = db.turns.find((other) => other.id === left.turn);
    emit(turn.id, 'turn.started', {});
    run(turn, script(left.script, turn.text), {answers: left.answers, instant: true});
  }
  // The turns that the example data leaves running are played up to where
  // they wait. That is how the data begins, and no change of it.
  setTimeout(() => {
    told = new Map(db.sessions.map((session) => [session.id, JSON.stringify(sessionRecord(session))]));
  }, 0);
}

// What the other member asks, in turn.
const ASKED = ['Which tests cover the checkout total?', 'Where do we retry a failed webhook?', 'What does the bundle rule do with a quantity of zero?'];

// elsewhere has another member of the project send a message to the agent, as
// someone at another screen would. It returns the name of the member and the
// title of the session, or null if nobody else can run a session there.
export function elsewhere(token, projectSlug, agentSlug) {
  const project = db.projects.find((other) => other.slug === projectSlug);
  const agents = project ? db.agents.filter((agent) => agent.project_id === project.id) : [];
  const agent = agents.find((other) => other.slug === agentSlug) || agents[0];
  if (!agent || !db.config.idp) return null;
  for (const [key, account] of Object.entries(db.accounts)) {
    if (key === token) continue;
    const who = identify(key);
    if (who.admin || !permissionsOf(project.id, who).includes('sessions.run')) continue;
    const words = ASKED[serial++ % ASKED.length];
    const busy = (session) => db.turns.some((turn) => turn.session_id === session.id && !turn.ended_at);
    let session = db.sessions.filter((other) => other.agent_id === agent.id && other.created_by === who.id && !other.ended_at && !busy(other))
      .sort((a, b) => (a.id < b.id ? 1 : -1))[0];
    if (!session) {
      session = {id: uuid7(), agent_id: agent.id, title: words, created_by: who.id, created_at: iso()};
      db.sessions.push(session);
    }
    accept(session, who, words, 'answer');
    changed();
    return {name: account.display_name, title: session.title};
  }
  return null;
}

// accounts lists the accounts of the mock's identity provider, for its
// sign-in page.
export function accounts() {
  return Object.entries(db.accounts).map(([token, account]) => ({token, ...account}));
}

// settings returns what the console is configured with: whether the
// installation has an identity provider, and its host.
export function settings() {
  return {idp: db.config.idp, host: db.config.host};
}

export function setIdentityProvider(on) {
  db.config.idp = on;
  mock.changed.emit();
}

reset();
setInterval(lull, 2500);
