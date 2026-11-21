// The overview of a project: where a person lands, picks an agent and goes
// back to what they were doing.

import {h, icon, use, code, plural, time, initials} from './util.js';
import {api} from './api.js';
import {app, project, can, person} from './state.js';
import {link, to} from './router.js';
import {agentTile, projectTile, label, chip, avatar, roleBadge, empty, loading} from './ui.js';
import {standing, standingText, title} from './sessions.js';
import {feed} from './feed.js';
import {agentCard, newAgent} from './v-agents.js';

function greeting() {
  const hour = new Date().getHours();
  const part = hour < 5 ? 'Good evening' : hour < 12 ? 'Good morning' : hour < 18 ? 'Good afternoon' : 'Good evening';
  const name = (app.me.display_name || '').split(' ')[0];
  return name ? `${part}, ${name}` : part;
}

// keyPill says where the key of a model provider is kept.
export function keyPill(context, provider) {
  const key = provider.api_key;
  if (!key) return h('span.pill', 'No key');
  if (key.stored) return h('span.pill.g', icon('lock'), 'Key stored');
  const store = context.stores.find((other) => other.id === key.store);
  return h('span.pill', {title: `${key.path} → ${key.key}`}, icon('key'), 'Key in ', store ? store.name : 'a secret store');
}

// what describes the action that an approval request asks about, in a few
// words.
export function wants(request) {
  const call = request.tool_call || {};
  const command = call.input && typeof call.input.command === 'string' ? call.input.command : '';
  if (command) return ['Wants to run ', code(command)];
  return ['Wants to: ', call.title || call.kind || 'act'];
}

const MARKS = {
  queued: 'clock', completed: 'check', failed: 'x', cancelled: 'cancel', expired: 'hour', ended: 'lock', none: 'chat',
};
const TONES = {none: 'idle', ended: 'ended'};

function sessionRow(context, {session, agent, stands}) {
  const live = ['starting', 'running', 'waiting', 'stopping'].includes(stands.key);
  const failure = stands.key === 'failed' && stands.turn?.failure;
  return h('a', {href: link(to.session(context.project, agent, session))},
    h('span.sti.st-' + (TONES[stands.key] || stands.key), live ? h('span.ring') : icon(MARKS[stands.key] || 'cancel', 'lg')),
    h('span.tx', title(session),
      h('small', agentTile(agent, 'xs'), label(agent), ' · ', time(session.created_at),
        failure && [' · ', h('span.err', failure.message || failure.reason)])),
    chip(TONES[stands.key] || stands.key, stands.key === 'waiting' ? 'Waiting for approval' : standingText(stands)));
}

// How many of the caller's sessions the overview shows.
const RECENT = 6;

const own = (session) => !app.config.idp || session.created_by === app.me.id;

// recent reads the caller's newest sessions in the project. The API lists
// sessions by agent, so the console asks every agent and merges.
async function recent(context) {
  if (!can(context, 'sessions.run') && !can(context, 'sessions.view_all')) return [];
  const lists = await Promise.all(context.agents.map(async (agent) => (await api.get(`/v1/agents/${agent.id}/sessions?limit=${RECENT}`, 'workaround')).sessions));
  return lists.flat().filter(own);
}

function start(context, add) {
  const manageConnections = can(context, 'connections.manage');
  const manageAgents = can(context, 'agents.manage');
  const connected = context.providers.length > 0;
  const step = (n, state, heading, text, action) => h('div.' + state, h('span.n', state === 'done' ? icon('check') : n), h('b', heading), h('p', text), state === 'now' && action);
  return h('div.card.start',
    step(1, connected ? 'done' : 'now', 'Add a model provider', 'The endpoint of a model, and its key.',
      manageConnections ? h('a.btn.pri.sm', {href: link(to.connections(context.project))}, 'Add a provider') : h('p.sub.sm', 'Ask someone who manages connections.')),
    step(2, !connected ? 'later' : 'now', 'Create an agent', 'A harness, a model and instructions.',
      manageAgents ? h('button.btn.pri.sm', {type: 'button', onclick: add}, 'New agent') : h('p.sub.sm', 'Ask someone who manages agents.')),
    step(3, 'later', 'Start a session', 'Send the agent a message.'));
}

export async function overviewView(root, params, env) {
  const context = await project(params.project);
  const {project: current, agents, providers, stores, members} = context;
  const add = () => newAgent(context);
  const manageAgents = can(context, 'agents.manage');

  let nudge = h('div', {hidden: true});
  const grid = h('div.agents');
  const drawAgents = (rows = []) => {
    const badge = (agent) => {
      const mine = rows.filter((row) => row.agent.id === agent.id).map((row) => row.stands.key);
      if (mine.includes('waiting')) return chip('waiting', 'Needs you');
      if (mine.some((key) => ['starting', 'running', 'stopping', 'queued'].includes(key))) return chip('running', 'Working');
      return null;
    };
    grid.replaceChildren(...agents.slice(0, 8).map((agent) => agentCard(context, agent, {badge: badge(agent)})));
  };
  drawAgents();
  const rowsBox = h('div.rows', h('div', {style: {padding: '12px 20px 16px', borderTop: '1px solid var(--line)'}}, loading(3)));

  const people = members.filter((member) => member.user?.id).length;
  const awaited = members.filter((member) => member.user && !member.user.id).length;
  const groups = members.filter((member) => member.group).length;
  const kinds = [people && plural(people, 'person', 'people'), groups && plural(groups, 'group'), awaited && `${awaited} awaited`].filter(Boolean).join(', ');
  const roles = ['owner', 'editor', 'user', 'viewer', 'auditor', 'custom'].filter((role) => members.some((member) => member.role === role));
  const face = (member) => {
    if (member.group) return h('span.av.grp', {title: 'Group ' + member.group}, icon('users'));
    if (!member.user.id) return h('span.av.wait', {title: member.user.username + ' has not signed in yet'}, initials(member.user.username)[0].toLowerCase());
    return avatar(person(context, member.user.id));
  };

  root.replaceChildren(h('section.page', h('div.wrap',
    h('div.hero',
      use('#dome', {class: 'ribs', viewBox: '0 0 600 400', 'aria-hidden': 'true'}),
      h('div', h('p.over', greeting()),
        h('h1', projectTile(current, 'lg'), label(current), current.name && code(current.slug)),
        h('p.facts',
          h('span', h('b', agents.length), agents.length === 1 ? 'agent' : 'agents'),
          h('span', h('b', providers.length), providers.length === 1 ? 'model provider' : 'model providers'),
          h('span', h('b', stores.length), stores.length === 1 ? 'secret store' : 'secret stores'),
          h('span', h('b', members.length), members.length === 1 ? 'member' : 'members'))),
      nudge),

    !agents.length && start(context, add),

    agents.length > 0 && [
      h('div.sec-h', h('h2.h2', 'Agents'), h('span.cnt', agents.length),
        agents.length > 8 && h('a.more', {href: link(to.agents(current))}, 'All agents'),
        manageAgents && h('button.btn.pri', {type: 'button', onclick: add}, icon('plus'), 'New agent')),
      grid,
    ],

    h('div.cols',
      h('div.card',
        h('div.sec-h', h('h2.h2', 'Your recent sessions')),
        rowsBox),
      h('div.card',
        h('div.sec-h', h('h2.h2', 'Connections'), h('a.more', {href: link(to.connections(current))}, can(context, 'connections.manage') ? 'Manage' : 'See all')),
        h('div.conn',
          h('h4', 'Model providers · ', providers.length),
          providers.length ? h('ul', providers.map((provider) => h('li',
            h('span.ic', icon('spark', 'lg')),
            h('span.tx', h('b', provider.name), code(provider.url)),
            keyPill(context, provider)))) : h('p.none', 'None yet'),
          h('h4', 'Secret stores · ', stores.length),
          stores.length ? h('ul', stores.map((store) => h('li',
            h('span.ic', icon('db', 'lg')),
            h('span.tx', h('b', store.name), code(`${store.settings?.address || ''} · mount ${store.settings?.mount || ''}`)),
            h('span.tag', store.kind)))) : h('p.none', 'None yet')))),

    h('div.card.mstrip',
      h('h2.h2', 'Members'),
      members.length ? h('div.stack', members.slice(0, 7).map(face)) : null,
      members.length
        ? h('p', h('b', plural(members.length, 'member')), kinds && ' · ' + kinds)
        : h('p', app.config.idp ? 'No members yet' : 'No sign-in: everyone is an administrator'),
      h('div.by', roles.map(roleBadge)),
      h('a.btn.sec.sm', {href: link(to.access(current))}, app.me.admin ? 'Manage access' : 'See access')),
  )));

  // The sessions come after the page, since they take a request for each
  // agent. From then on they follow the stream of changes.
  let sessions = [];
  const drawSessions = () => {
    sessions = sessions.sort((a, b) => (a.id < b.id ? 1 : -1)).slice(0, RECENT);
    const rows = sessions.map((session) => ({session, agent: agents.find((agent) => agent.id === session.agent_id), stands: standing(session)}))
      .filter((row) => row.agent);
    drawAgents(rows);
    const waiting = rows.find((row) => row.stands.key === 'waiting');
    const next = !waiting ? h('div', {hidden: true}) : h('a.nudge', {href: link(to.session(current, waiting.agent, waiting.session))},
      h('span', icon('shield', 'lg')),
      h('div.tx', h('b', label(waiting.agent), ' is waiting for you'), h('small', wants(waiting.stands.turn.approvals[0]))),
      h('span.btn.pri.sm', 'Review'));
    nudge.replaceWith(next);
    nudge = next;
    rowsBox.replaceChildren(...(rows.length ? rows.map((row) => sessionRow(context, row))
      : [h('div', {style: {borderTop: '1px solid var(--line)'}}, empty('chat', 'No sessions yet',
        can(context, 'sessions.run') ? 'Open an agent to start one.' : null))]));
  };
  // What changed since the sessions were last read is newer than what is
  // read.
  const changed = new Set();
  const load = async () => {
    await feed.ready();
    changed.clear();
    const read = await recent(context);
    if (env.stale()) return;
    for (const session of read) feed.note(session);
    sessions = [...sessions.filter((session) => changed.has(session.id)), ...read.filter((session) => !changed.has(session.id))];
    drawSessions();
  };
  const unwatch = [
    feed.sessions.on((session) => {
      if (!own(session)) return;
      changed.add(session.id);
      sessions = [session, ...sessions.filter((other) => other.id !== session.id)];
      drawSessions();
    }),
    feed.stale.on(() => load().catch(() => {})),
  ];
  load().catch((error) => { if (!env.stale()) rowsBox.replaceChildren(h('p.sub', {style: {padding: '12px 20px'}}, 'Could not load the sessions: ' + error.message)); });

  return {context, cleanup: () => { for (const off of unwatch) off(); }};
}
