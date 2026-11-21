// Routes of the console, kept in the fragment of the URL so that the mock
// works from a file. The product can keep them in the path instead.

const routes = [];

// on adds a route. A pattern is a path with parameters, such as
// "/p/:project/agents/:agent". name tells the shell which tab is in view.
export function on(pattern, view, name = '') {
  const keys = [];
  const expression = new RegExp('^' + pattern.replace(/:(\w+)/g, (_, key) => { keys.push(key); return '([^/]+)'; }) + '/?$');
  routes.push({pattern, expression, keys, view, name});
}

export function path() {
  return decodeURI(location.hash.replace(/^#/, '')) || '/';
}

// resolve returns the route of the current URL: {view, name, params, path}.
export function resolve() {
  const [plain] = path().split('?');
  for (const route of routes) {
    const match = plain.match(route.expression);
    if (!match) continue;
    const params = {};
    route.keys.forEach((key, index) => { params[key] = decodeURIComponent(match[index + 1]); });
    return {view: route.view, name: route.name, pattern: route.pattern, params, path: plain};
  }
  return null;
}

export function link(to) {
  return '#' + encodeURI(to);
}

// go opens a path. replace changes the URL without adding to the history.
export function go(to, {replace = false} = {}) {
  const target = link(to);
  if (location.hash === target) {
    window.dispatchEvent(new HashChangeEvent('hashchange'));
  } else if (replace) {
    location.replace(target);
  } else {
    location.hash = target;
  }
}

// Paths of the console.
export const to = {
  projects: () => '/projects',
  project: (project) => `/p/${project.slug}`,
  agents: (project) => `/p/${project.slug}/agents`,
  agent: (project, agent) => `/p/${project.slug}/agents/${agent.slug}`,
  session: (project, agent, session) => `/p/${project.slug}/agents/${agent.slug}/s/${session.id}`,
  connections: (project) => `/p/${project.slug}/connections`,
  access: (project) => `/p/${project.slug}/access`,
};
