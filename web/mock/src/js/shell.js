// The shell: the bar at the top of every signed-in screen, and the place
// below it where a view draws.

import {h, icon, use} from './util.js';
import {app, theme, brand, forget, can, controls} from './state.js';
import {link, to, go} from './router.js';
import {menu, item, avatar, projectTile, label, adminBadge, segmented, toast} from './ui.js';
import {signIn} from './api.js';
import {feed} from './feed.js';
import {newProject} from './v-projects.js';

let bar;
let view;
let shown = {route: null, context: null};

export function mount(root) {
  bar = h('header.nav');
  view = h('main#view');
  root.replaceChildren(bar, view);
  app.changed.on(() => draw(shown.route, shown.context));
  feed.sessions.on(announce);
  return view;
}

// The approval requests that the caller has been told of.
const announced = new Set();

// announce tells the caller that a turn waits for an answer that the caller
// may give, wherever in the project the caller is. A request of the session
// in view shows in the conversation itself.
function announce(session) {
  const {context, route} = shown;
  const turn = session.last_turn;
  if (!context || !turn || turn.ended_at) return;
  const fresh = (turn.approvals || []).filter((request) => !announced.has(turn.id + '/' + request.id));
  for (const request of fresh) announced.add(turn.id + '/' + request.id);
  const agent = context.agents.find((other) => other.id === session.agent_id);
  if (!fresh.length || !agent || !controls(context, turn) || route?.params?.sid === session.id) return;
  toast([h('b', label(agent)), ' is waiting for you'], '', 9000, h('a', {href: link(to.session(context.project, agent, session))}, 'Review'));
}

export function signOut() {
  signIn('');
  forget();
  go('/signin');
}

// draw shows the bar for a route. context is the project in view, if any.
// A route without a name, such as the sign-in screen, has no bar.
export function draw(route, context) {
  shown = {route, context};
  bar.hidden = !route || route.name === 'bare' || !app.me;
  if (bar.hidden) return;
  const project = context?.project;
  const home = project ? to.project(project) : to.projects();
  const tabs = project && [
    ['overview', 'Overview', to.project(project)],
    ['agents', 'Agents', to.agents(project)],
    ['connections', 'Connections', to.connections(project)],
    ['access', 'Access', to.access(project)],
  ];

  const switcher = h('button.sw-b' + (project ? '' : '.none'), {type: 'button', 'aria-haspopup': 'true', 'aria-expanded': 'false', onclick: () => menu(switcher, projects, {cls: 'sw'})},
    project ? [projectTile(project), h('span', label(project))] : h('span', 'Choose a project'), icon('updown'));
  const projects = (close) => [
    h('small.h', 'Projects'),
    h('div.list', (app.projects || []).map((other) => item({
      lead: projectTile(other), text: label(other), on: project?.id === other.id, href: link(to.project(other)),
    }, close))),
    !(app.projects || []).length && h('div.row', 'You are in no project yet.'),
    h('hr'),
    item({glyph: 'grid', text: 'All projects', href: link(to.projects())}, close),
    app.me.admin && h('button.it.new', {type: 'button', onclick: () => { close(); newProject(); }},
      h('span.box', icon('plus')), h('span.nm', 'New project')),
  ];

  const dark = theme.dark();
  const flip = h('button.ib', {type: 'button', title: dark ? 'Switch to the light theme' : 'Switch to the dark theme', onclick: () => theme.set(dark ? 'light' : 'dark')}, icon(dark ? 'sun' : 'moon', 'lg'));

  bar.replaceChildren(...[
    h('a.logo', {href: link(home), title: 'Aviary'}, use(brand.logo()), 'Aviary'),
    h('span.slash'),
    switcher,
    tabs && h('nav.navt', {'aria-label': 'Project'}, tabs.map(([name, text, path]) => h('a' + (route.name === name ? '.on' : ''), {href: link(path)}, text))),
    h('div.nav-r', flip, account()),
  ].filter(Boolean));
}

function themeRow() {
  return h('div.row', 'Theme', segmented([['system', 'Auto', 'monitor'], ['light', 'Light', 'sun'], ['dark', 'Dark', 'moon']], theme.get(), (value) => theme.set(value)));
}

function account() {
  if (!app.config.idp) {
    const open = h('button.me.open', {type: 'button', 'aria-haspopup': 'true', onclick: () => menu(open, () => [
      h('div.who', h('div', h('b', 'No sign-in'), h('small', 'Everyone is an administrator here.'))),
      h('hr'), themeRow(),
    ], {align: 'right', cls: 'acct'})}, icon('user'), 'No sign-in', icon('down'));
    return open;
  }
  const me = app.me;
  const who = {name: me.display_name || me.username || 'You', known: true, key: me.id};
  const open = h('button.me', {type: 'button', 'aria-haspopup': 'true', onclick: () => menu(open, (close) => [
    h('div.who', avatar(who, 'lg'), h('div', h('b', who.name), h('small', me.username || me.subject))),
    h('hr'),
    item({glyph: 'user', text: 'Your account', href: link('/me')}, close),
    themeRow(),
    h('hr'),
    item({glyph: 'logout', text: 'Sign out', onclick: signOut}, close),
  ], {align: 'right', cls: 'acct'})}, avatar(who, 'sm'), h('span.nm', who.name), me.admin && adminBadge(), icon('down'));
  return open;
}

// allowed is a small helper for views: whether the caller has the permission
// in the project in view.
export function allowed(permission) {
  return shown.context ? can(shown.context, permission) : false;
}
