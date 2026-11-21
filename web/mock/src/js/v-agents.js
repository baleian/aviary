// The agents of a project, and the dialog that creates one.

import {h, icon, time, code} from './util.js';
import {api} from './api.js';
import {project, refresh, can, loadHarnesses} from './state.js';
import {link, to, go} from './router.js';
import {dialog, confirm, field, select, toggle, wrong, agentTile, label, empty, toast, menu, item} from './ui.js';
import {named} from './v-projects.js';

export function providerOf(context, agent) {
  return context.providers.find((provider) => provider.id === agent.model.provider);
}

// agentCard is the card of an agent. badge replaces the arrow in its corner,
// and more, a button, takes the corner for itself.
export function agentCard(context, agent, {badge, full = false, more} = {}) {
  const provider = providerOf(context, agent);
  const card = h('a.card.agent', {href: link(to.agent(context.project, agent))},
    h('div.top', agentTile(agent, 'xl'), !more && (badge || h('span.go', icon('out')))),
    h('h3', label(agent), full && agent.name ? h('span.slug', agent.slug) : null),
    h('p.model', provider ? provider.name : 'Unknown provider', ' / ', code(agent.model.name)),
    full && h('p.ins' + (agent.description ? '' : '.none'), agent.description || 'No description'),
    h('ul',
      h('li', icon('term'), h('span', code(agent.harness), ' harness')),
      agent.approval === 'ask'
        ? h('li.ask', icon('shield'), h('span', 'Asks before it acts'))
        : h('li', icon('bolt'), h('span', 'Acts without asking')),
      full && h('li', icon('clock'), h('span', 'Created ', time(agent.created_at)))));
  return more ? h('div.ag-w', card, more) : card;
}

// agentMenu is the button that opens the menu of an agent, for a caller who
// manages the agents. changed is called after the agent has changed, with
// true if it was deleted.
export function agentMenu(context, agent, changed, size = '') {
  if (!can(context, 'agents.manage')) return null;
  const more = h('button.ib' + (size ? '.' + size : ''), {type: 'button', title: 'More', 'aria-haspopup': 'true', onclick: () => menu(more, (close) => [
    item({glyph: 'edit', text: 'Edit agent', onclick: async () => { if (await editAgent(context, agent)) changed(false); }}, close),
    h('hr'),
    item({glyph: 'trash', text: 'Delete agent…', danger: true, onclick: async () => { if (await deleteAgent(context, agent)) changed(true); }}, close),
  ], {align: 'right'})}, icon('dots'));
  return more;
}

// deleteAgent deletes the agent once the caller has confirmed it, and
// reports whether it did.
async function deleteAgent(context, agent) {
  const deleted = await confirm({
    title: `Delete ${label(agent)}?`,
    lead: 'The sessions of every member end, and the files that the agent made in them are deleted.',
    action: 'Delete agent', danger: true,
    submit: async () => {
      try {
        return await api.delete('/v1/agents/' + agent.id);
      } catch (error) {
        if (error.code === 'session_busy') throw wrong('Stop its running turns first.');
        throw error;
      }
    },
  });
  if (!deleted) return false;
  await refresh(context, 'agents');
  toast(`Deleted ${label(agent)}`, 'ok');
  return true;
}

// editAgent creates an agent, or replaces one: all of it but its slug and its
// harness, which stay as they are. It returns the agent, or null if the
// dialog was dismissed.
export async function editAgent(context, existing) {
  if (!context.providers.length) {
    const manage = can(context, 'connections.manage');
    const open = await confirm({
      title: 'Add a model provider first',
      lead: manage ? 'An agent uses a model of one of the project’s providers.' : 'Ask someone who manages the connections of this project.',
      action: manage ? 'Add a model provider' : 'See connections',
    });
    if (open) go(to.connections(context.project));
    return null;
  }
  const harnesses = existing ? [] : await loadHarnesses();
  const names = named('Backend engineer');
  names.name.value = existing?.name || '';
  const description = h('input.inp', {name: 'description', value: existing?.description || '', placeholder: 'Features, fixes and tests for the storefront backend.', autocomplete: 'off', maxlength: 200});
  const harness = select({name: 'harness'}, harnesses.map((each) => [each.name, each.name]));
  const provider = select({name: 'provider'}, context.providers.map((each) => [each.id, each.name, existing?.model.provider === each.id]));
  const model = h('input.inp.mono', {name: 'model', value: existing?.model.name || '', placeholder: 'coder-large', autocomplete: 'off', spellcheck: 'false'});
  const instructions = h('textarea.inp', {name: 'instructions', rows: 4, placeholder: 'You work on the storefront backend. Keep changes small, and say which tests you ran.'}, existing?.instructions || '');
  const approval = toggle({name: 'approval', checked: existing?.approval === 'ask'}, 'Ask before it acts');

  const saved = await dialog({
    title: existing ? 'Edit ' + label(existing) : 'New agent',
    size: 'wide',
    action: existing ? 'Save agent' : 'Create agent',
    body: [
      existing ? field('Name', names.name, {optional: true}) : names.fields,
      field('Description', description, {optional: true}),
      existing
        ? h('div.fld-row', field('Model provider', provider), field('Model', model))
        : [h('div.fld-row', field('Harness', harness), field('Model provider', provider)), field('Model', model)],
      field('Instructions', instructions, {optional: true}),
      h('div.fld', approval),
    ],
    submit: () => {
      const named = existing ? {name: names.name.value.trim()} : names.read();
      if (!model.value.trim()) throw wrong('Enter the model.');
      const body = {
        ...named, description: description.value.trim(),
        model: {provider: provider.firstChild.value, name: model.value.trim()},
        instructions: instructions.value.trim(),
        approval: approval.firstChild.checked ? 'ask' : '',
      };
      if (existing) return api.put('/v1/agents/' + existing.id, body);
      return api.post(`/v1/projects/${context.project.id}/agents`, {...body, harness: harness.firstChild.value});
    },
  });
  if (!saved) return null;
  await refresh(context, 'agents');
  toast(existing ? `Saved ${label(saved)}` : `Created ${label(saved)}`, 'ok');
  return saved;
}

export async function newAgent(context) {
  const created = await editAgent(context);
  if (created) go(to.agent(context.project, created));
}

export async function agentsView(root, params) {
  const context = await project(params.project);
  const manage = can(context, 'agents.manage');
  const add = () => newAgent(context);
  const again = () => go(to.agents(context.project));
  root.replaceChildren(h('section.page', h('div.wrap',
    h('div.pg-h',
      h('div', h('p.over', label(context.project)), h('h1.h1', 'Agents')),
      h('div.act', manage && h('button.btn.pri', {type: 'button', onclick: add}, icon('plus'), 'New agent'))),
    context.agents.length
      ? h('div.ag-grid', context.agents.map((agent) => agentCard(context, agent, {full: true, more: agentMenu(context, agent, again)})),
        manage && h('button.card.agent.new', {type: 'button', onclick: add}, h('span.box', icon('plus', 'lg')), 'New agent'))
      : h('div.card', manage
        ? empty('spark', 'No agents yet', null, h('button.btn.pri', {type: 'button', onclick: add}, icon('plus'), 'New agent'))
        : empty('spark', 'No agents yet')),
  )));
  return {context};
}
