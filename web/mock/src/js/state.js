// What the console knows about the caller and the project in view, and how
// it loads it.

import {api, installation, signedInAs} from './api.js';
import {emitter, prefs, shortID} from './util.js';

export const ALL = ['project.view', 'agents.manage', 'connections.manage', 'sessions.run', 'sessions.view_all', 'sessions.control_all'];

// What a permission allows, in the words of the console.
export const ALLOWS = {
  'project.view': 'See the project',
  'agents.manage': 'Manage agents',
  'connections.manage': 'Manage connections',
  'sessions.run': 'Run sessions',
  'sessions.view_all': 'See every session',
  'sessions.control_all': 'Control every session',
};

export const ROLE_NOTES = {
  owner: 'Runs the project', editor: 'Builds agents and connections', user: 'Works with the agents',
  viewer: 'Looks, and touches nothing', auditor: 'Reads every session',
};

export const app = {
  // Whether the installation has an identity provider, and its host.
  config: installation(),
  // The caller, from GET /v1/me. null until it is loaded.
  me: null,
  // The reason, if the caller is signed in but may not use the installation.
  shut: null,
  projects: null,
  roles: null,
  harnesses: null,
  // Emitted when something that the shell shows has changed.
  changed: emitter(),
};

const contexts = new Map();

// tint numbers the records of a list in their order, which is the order in
// which they were created. Tiles take their colour from it, so that
// neighbours differ.
function tint(list) {
  list.forEach((record, index) => Object.defineProperty(record, 'tint', {value: index, enumerable: false, configurable: true}));
  return list;
}

export function forget() {
  Object.assign(app, {config: installation(), me: null, shut: null, projects: null, roles: null, harnesses: null});
  contexts.clear();
}

// enter loads the caller. It returns "in" if the caller may use the console,
// "out" if nobody is signed in, and "shut" if the caller is signed in but in
// no group that may use the installation.
export async function enter() {
  app.config = installation();
  if (app.me && !app.shut) return 'in';
  if (app.config.idp && !signedInAs()) return 'out';
  try {
    app.me = await api.get('/v1/me');
  } catch (error) {
    if (error.status === 401) return 'out';
    throw error;
  }
  try {
    app.projects = tint((await api.get('/v1/projects')).projects);
    app.shut = null;
  } catch (error) {
    if (error.status !== 403) throw error;
    app.shut = error;
    return 'shut';
  }
  app.changed.emit();
  return 'in';
}

export async function loadProjects() {
  app.projects = tint((await api.get('/v1/projects')).projects);
  app.changed.emit();
  return app.projects;
}

export async function loadRoles(fresh = false) {
  if (!app.roles || fresh) app.roles = (await api.get('/v1/roles')).roles;
  return app.roles;
}

export async function loadHarnesses() {
  if (!app.harnesses) app.harnesses = (await api.get('/v1/harnesses')).harnesses;
  return app.harnesses;
}

// NotFound is thrown for a project or an agent that the caller cannot see.
// The API does not tell a missing one from a hidden one, and neither does
// the console.
export class NotFound extends Error {}

// project returns the context of the project with the slug: the project and
// what the console shows of it on every screen. It is loaded once, and again
// after refresh().
export async function project(slug) {
  if (!app.projects) await loadProjects();
  const found = app.projects.find((other) => other.slug === slug);
  if (!found) throw new NotFound(slug);
  if (!contexts.has(found.id)) contexts.set(found.id, load(found));
  try {
    return await contexts.get(found.id);
  } catch (error) {
    contexts.delete(found.id);
    if (error.status === 404) throw new NotFound(slug);
    throw error;
  }
}

async function load(found) {
  const base = '/v1/projects/' + found.id;
  const [agents, providers, stores, members, roles] = await Promise.all([
    api.get(base + '/agents'), api.get(base + '/providers'), api.get(base + '/secret-stores'), api.get(base + '/members'), loadRoles(),
  ]);
  const context = {project: found, agents: tint(agents.agents), providers: providers.providers, stores: stores.secret_stores, members: members.members, roles};
  context.permissions = permissionsIn(context);
  return context;
}

// refresh loads parts of a project's context again, after a change.
export async function refresh(context, ...parts) {
  const base = '/v1/projects/' + context.project.id;
  await Promise.all(parts.map(async (part) => {
    if (part === 'agents') context.agents = tint((await api.get(base + '/agents')).agents);
    if (part === 'providers') context.providers = (await api.get(base + '/providers')).providers;
    if (part === 'stores') context.stores = (await api.get(base + '/secret-stores')).secret_stores;
    if (part === 'members') context.members = (await api.get(base + '/members')).members;
    if (part === 'roles') context.roles = await loadRoles(true);
  }));
  context.permissions = permissionsIn(context);
  app.changed.emit();
}

// permissionsIn works out what the caller may do in the project, from its
// members and the roles. The API has no request that returns it.
function permissionsIn(context) {
  if (app.me.admin) return [...ALL];
  const set = new Set();
  for (const member of context.members) {
    const mine = member.user ? member.user.id === app.me.id : (app.me.groups || []).includes(member.group);
    if (!mine) continue;
    const granted = member.role === 'custom' ? member.permissions : context.roles.find((role) => role.name === member.role)?.permissions || [];
    for (const permission of granted) set.add(permission);
  }
  return ALL.filter((permission) => set.has(permission));
}

export function can(context, permission) {
  return context.permissions.includes(permission);
}

// mine returns the members of the project through which the caller is in it.
export function mine(context) {
  return context.members.filter((member) => (member.user ? member.user.id === app.me.id : (app.me.groups || []).includes(member.group)));
}

// person returns how to show the user with an ID: {name, known, key, me}.
// The API reports users by ID. The console knows the caller and the members
// of the project that are users, and nobody else: not an administrator who is
// no member, and not the people of a member group.
export function person(context, id) {
  if (!id) return {name: 'Nobody', known: false};
  if (app.me && id === app.me.id) return {name: app.me.display_name || app.me.username || 'You', known: true, key: id, me: true};
  const member = context?.members.find((other) => other.user?.id === id);
  if (member) return {name: member.user.display_name || member.user.username, known: true, key: id};
  return {name: 'User ' + shortID(id), known: false, key: id, title: 'User ' + id};
}

// sessionAccess returns what the caller may do with a session.
export function sessionAccess(context, session) {
  const own = !app.config.idp || session.created_by === app.me.id;
  const admin = app.me.admin;
  const control = admin || (own ? can(context, 'sessions.run') : can(context, 'sessions.control_all'));
  return {own, send: admin || (own && can(context, 'sessions.run')), control};
}

// ---- Settings of the browser ----------------------------------------------

export const theme = {
  get: () => prefs.get('theme', 'system'),
  set(value) {
    prefs.set('theme', value);
    theme.apply();
    app.changed.emit();
  },
  dark() {
    const chosen = theme.get();
    return chosen === 'dark' || (chosen === 'system' && matchMedia('(prefers-color-scheme: dark)').matches);
  },
  apply() {
    document.documentElement.dataset.theme = theme.dark() ? 'dark' : 'light';
  },
};
matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => { theme.apply(); app.changed.emit(); });

// The first is the mark that was chosen. The others were drawn beside it.
export const MARKS = ['flight', 'beacon', 'perch', 'sharp', 'wing'];

// brand is the mark of the logo. The mock offers several, to compare them in
// place.
export const brand = {
  get() {
    const chosen = prefs.get('logo', MARKS[0]);
    return MARKS.includes(chosen) ? chosen : MARKS[0];
  },
  set(value) {
    prefs.set('logo', value);
    brand.apply();
    app.changed.emit();
  },
  logo: () => '#logo-' + brand.get(),
  mark: () => '#mark-' + brand.get(),
  // apply sets the icon of the tab to the logo.
  apply() {
    const source = document.getElementById('logo-' + brand.get());
    const mark = document.getElementById('mark-' + brand.get());
    if (!source || !mark) return;
    const box = mark.getAttribute('viewBox');
    const placed = source.querySelector('use');
    const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48"><defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#2E90FA"/><stop offset="1" stop-color="#0A5FD6"/></linearGradient></defs>` +
      `<rect width="48" height="48" rx="13" fill="url(#g)"/><svg x="${placed.getAttribute('x')}" y="${placed.getAttribute('y')}" width="${placed.getAttribute('width')}" height="${placed.getAttribute('height')}" viewBox="${box}" style="color:#fff;--dot:#FFC53D">${mark.innerHTML}</svg></svg>`;
    document.getElementById('favicon').href = 'data:image/svg+xml,' + encodeURIComponent(svg);
  },
};
