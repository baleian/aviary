// The list of projects, and the dialog that creates one.

import {h, icon, time, slugify, isSlug} from './util.js';
import {api} from './api.js';
import {app, loadProjects} from './state.js';
import {link, to, go} from './router.js';
import {dialog, field, wrong, projectTile, label, empty, toast} from './ui.js';

// named returns the two fields that name a project or an agent: its name,
// for people, and its slug, for URLs. The slug follows the name until it is
// edited by hand.
export function named(example) {
  const name = h('input.inp', {name: 'name', placeholder: example, autocomplete: 'off', autofocus: true, maxlength: 120});
  const slug = h('input.inp.mono', {name: 'slug', placeholder: slugify(example), autocomplete: 'off', spellcheck: 'false', maxlength: 63});
  let own = false;
  name.addEventListener('input', () => { if (!own) slug.value = slugify(name.value); });
  slug.addEventListener('input', () => { own = slug.value !== ''; });
  return {
    name, slug,
    fields: h('div.fld-row',
      field('Name', name, {optional: true}),
      field('Slug', slug, {hint: 'Lowercase letters, digits and hyphens. It cannot be changed later.'})),
    // read returns the two values, or throws if the slug is not one.
    read() {
      const value = slug.value.trim();
      if (!value) throw wrong('Enter a slug.');
      if (!isSlug(value)) throw wrong('A slug is 1 to 63 lowercase letters, digits and hyphens. It begins with a letter and does not end with a hyphen.');
      return {slug: value, name: name.value.trim()};
    },
  };
}

export async function newProject() {
  const names = named('Storefront');
  const created = await dialog({
    title: 'New project',
    body: names.fields,
    action: 'Create project',
    submit: () => api.post('/v1/projects', names.read()),
  });
  if (!created) return;
  await loadProjects();
  toast(`Created ${label(created)}`, 'ok');
  go(to.project(created));
}

export async function projectsView(root) {
  const projects = await loadProjects();
  const admin = app.me.admin;
  const add = admin && h('button.btn.pri', {type: 'button', onclick: newProject}, icon('plus'), 'New project');
  const card = (project) => h('a.card.proj', {href: link(to.project(project))},
    projectTile(project, 'lg'),
    h('div.tx', h('h3', label(project)), project.name && h('code', project.slug), h('small', 'Created ', time(project.created_at))),
    h('span.go', icon('arrow')));

  root.replaceChildren(h('section.page', h('div.wrap',
    h('div.pg-h', h('h1.h1', 'Projects'), h('div.act', add)),
    projects.length
      ? h('div.projects', projects.map(card), admin && h('button.card.proj.new', {type: 'button', onclick: newProject}, h('span.box', icon('plus', 'lg')), 'New project'))
      : h('div.card', admin
        ? empty('folder', 'No projects yet', null, h('button.btn.pri', {type: 'button', onclick: newProject}, icon('plus'), 'New project'))
        : empty('folder', 'No projects yet', 'Ask an administrator to add you to one.')),
  )));
}
