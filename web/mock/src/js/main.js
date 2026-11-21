// Starts the console: its routes, and what is drawn for each.

import {h, icon, prefs} from './util.js';
import {unauthenticated} from './api.js';
import {app, enter, forget, theme, brand, NotFound} from './state.js';
import {feed} from './feed.js';
import {on, resolve, go, path, link} from './router.js';
import {mount, draw, signOut} from './shell.js';
import {blank, label, sentence, loading} from './ui.js';
import {signinView} from './v-signin.js';
import {projectsView} from './v-projects.js';
import {overviewView} from './v-overview.js';
import {agentsView} from './v-agents.js';
import {workspaceView} from './v-session.js';
import {connectionsView} from './v-connections.js';
import {accessView} from './v-access.js';
import {meView, identity} from './v-me.js';
import {brandView} from './v-brand.js';
import {mountBar} from './mockbar.js';

on('/signin', signinView, 'bare');
on('/projects', projectsView, 'projects');
on('/p/:project', overviewView, 'overview');
on('/p/:project/agents', agentsView, 'agents');
on('/p/:project/agents/:agent', workspaceView, 'agents');
on('/p/:project/agents/:agent/s/:sid', workspaceView, 'agents');
on('/p/:project/connections', connectionsView, 'connections');
on('/p/:project/access', accessView, 'access');
on('/me', meView, 'me');
on('/brand', brandView, 'brand');

const TITLES = {overview: 'Overview', agents: 'Agents', connections: 'Connections', access: 'Access', projects: 'Projects', me: 'Your account', brand: 'Brand'};

const root = mount(document.getElementById('app'));
let shown = null;
let turn = 0;

// shut is the screen of a caller who is signed in but in no group that may
// use the installation.
function shut() {
  return blank({
    full: true, glyph: 'lock', tone: 'y', title: 'You do not have access',
    text: 'Your account is in no group that may use this installation. Ask an administrator.',
    more: h('div.card.facts', {style: {padding: '18px 20px'}}, identity(app.me)),
    actions: h('button.btn.sec', {type: 'button', onclick: signOut}, icon('logout'), 'Sign out'),
  });
}

async function render() {
  const mine = ++turn;
  const stale = () => mine !== turn;
  const route = resolve();
  const open = route?.name === 'brand';
  let state;
  try {
    state = await enter();
  } catch (error) {
    console.error(error);
    root.replaceChildren(blank({full: true, glyph: 'warn', title: 'The console cannot reach Aviary', text: sentence(error.message)}));
    return;
  }
  if (stale()) return;
  if (state === 'out' && !open) {
    if (route?.name !== 'bare') {
      if (path() !== '/') prefs.set('return', path());
      go('/signin', {replace: true});
      return;
    }
  } else if (state === 'shut' && !open) {
    shown?.cleanup?.();
    shown = null;
    draw({name: 'bare'}, null);
    root.replaceChildren(shut());
    return;
  } else if (state === 'in' && (!route || route.name === 'bare') && (path() === '/' || route?.name === 'bare')) {
    // After signing in, back to where the caller was going.
    const back = prefs.get('return');
    prefs.set('return', null);
    const last = prefs.get('project');
    const known = (app.projects || []).find((project) => project.slug === last) || (app.projects || []).length === 1 && app.projects[0];
    go(back || (known ? '/p/' + known.slug : '/projects'), {replace: true});
    return;
  }

  // The stream of changes is that of the project in view, and is open before
  // the view reads anything.
  const viewed = route?.params.project && (app.projects || []).find((project) => project.slug === route.params.project);
  feed.watch(viewed ? viewed.id : null);

  if (!route) {
    shown?.cleanup?.();
    shown = null;
    draw({name: ''}, null);
    root.replaceChildren(blank({glyph: 'search', title: 'Not found', actions: h('a.btn.pri', {href: link('/projects')}, 'Go to the projects')}));
    return;
  }
  // A view can take a new address itself, as the workspace does for another
  // session of the same agent.
  if (shown && shown.view === route.view && shown.update?.(route.params)) {
    draw(route, shown.context);
    return;
  }
  shown?.cleanup?.();
  shown = null;
  if (!root.firstChild) root.replaceChildren(h('div.wrap', {style: {paddingTop: '48px'}}, loading(4)));
  try {
    const result = (await route.view(root, route.params, {stale})) || {};
    if (stale()) {
      result.cleanup?.();
      return;
    }
    shown = {view: route.view, ...result};
    draw(route, result.context);
    if (result.context) prefs.set('project', result.context.project.slug);
    document.title = [TITLES[route.name], result.context && label(result.context.project), 'Aviary'].filter(Boolean).join(' · ');
    if (route.view !== workspaceView) window.scrollTo(0, 0);
  } catch (error) {
    if (stale()) return;
    const missing = error instanceof NotFound || error.status === 404;
    if (!missing) console.error(error);
    draw({name: ''}, null);
    root.replaceChildren(blank(missing ? {
      glyph: 'search', title: 'Not found',
      text: 'It does not exist, or you do not have access to it.',
      actions: h('a.btn.pri', {href: link('/projects')}, 'Go to your projects'),
    } : {
      glyph: 'warn', title: error.status === 403 ? 'You may not do this' : 'Something went wrong',
      text: sentence(error.message), actions: h('a.btn.sec', {href: link('/projects')}, 'Go to your projects'),
    }));
  }
}

window.addEventListener('hashchange', render);
// A request that is refused for want of a token means that the sign-in has
// run out.
unauthenticated.on(() => {
  if (!app.me) return;
  forget();
  go('/signin', {replace: true});
});

theme.apply();
brand.apply();
mountBar(() => { shown?.cleanup?.(); shown = null; root.replaceChildren(); render(); });
render();
