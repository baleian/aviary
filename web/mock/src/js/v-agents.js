// The agents of a project, and the dialog that creates one.

import {h, icon, time, code} from './util.js';
import {api} from './api.js';
import {project, refresh, can, loadHarnesses} from './state.js';
import {link, to, go} from './router.js';
import {dialog, confirm, field, select, toggle, wrong, agentTile, label, empty, toast} from './ui.js';
import {named} from './v-projects.js';

export function providerOf(context, agent) {
  return context.providers.find((provider) => provider.id === agent.model.provider);
}

// agentCard is the card of an agent. badge replaces the arrow in its corner.
export function agentCard(context, agent, {badge, full = false} = {}) {
  const provider = providerOf(context, agent);
  return h('a.card.agent', {href: link(to.agent(context.project, agent))},
    h('div.top', agentTile(agent, 'xl'), badge || h('span.go', icon('out'))),
    h('h3', label(agent), full && agent.name ? h('span.slug', agent.slug) : null),
    h('p.model', provider ? provider.name : 'Unknown provider', ' / ', code(agent.model.name)),
    full && h('p.ins' + (agent.description ? '' : '.none'), agent.description || 'No description'),
    h('ul',
      h('li', icon('term'), h('span', code(agent.harness), ' harness')),
      agent.approval === 'ask'
        ? h('li.ask', icon('shield'), h('span', 'Asks before it acts'))
        : h('li', icon('bolt'), h('span', 'Acts without asking')),
      full && h('li', icon('clock'), h('span', 'Created ', time(agent.created_at)))));
}

export async function newAgent(context) {
  if (!context.providers.length) {
    const manage = can(context, 'connections.manage');
    const open = await confirm({
      title: 'Add a model provider first',
      lead: manage ? 'An agent uses a model of one of the project’s providers.' : 'Ask someone who manages the connections of this project.',
      action: manage ? 'Add a model provider' : 'See connections',
    });
    if (open) go(to.connections(context.project));
    return;
  }
  const harnesses = await loadHarnesses();
  const names = named('Backend engineer');
  const description = h('input.inp', {name: 'description', placeholder: 'Features, fixes and tests for the storefront backend.', autocomplete: 'off', maxlength: 200});
  const harness = select({name: 'harness'}, harnesses.map((each) => [each.name, each.name]));
  const provider = select({name: 'provider'}, context.providers.map((each) => [each.id, each.name]));
  const model = h('input.inp.mono', {name: 'model', placeholder: 'coder-large', autocomplete: 'off', spellcheck: 'false'});
  const instructions = h('textarea.inp', {name: 'instructions', rows: 4, placeholder: 'You work on the storefront backend. Keep changes small, and say which tests you ran.'});
  const approval = toggle({name: 'approval'}, 'Ask before it acts');

  const created = await dialog({
    title: 'New agent',
    size: 'wide',
    action: 'Create agent',
    body: [
      names.fields,
      field('Description', description, {optional: true}),
      h('div.fld-row', field('Harness', harness), field('Model provider', provider)),
      field('Model', model),
      field('Instructions', instructions, {optional: true}),
      h('div.fld', approval),
    ],
    submit: () => {
      const {slug, name} = names.read();
      if (!model.value.trim()) throw wrong('Enter the model.');
      return api.post(`/v1/projects/${context.project.id}/agents`, {
        slug, name, description: description.value.trim(), harness: harness.firstChild.value,
        model: {provider: provider.firstChild.value, name: model.value.trim()},
        instructions: instructions.value.trim(),
        approval: approval.firstChild.checked ? 'ask' : '',
      });
    },
  });
  if (!created) return;
  await refresh(context, 'agents');
  toast(`Created ${label(created)}`, 'ok');
  go(to.agent(context.project, created));
}

export async function agentsView(root, params) {
  const context = await project(params.project);
  const manage = can(context, 'agents.manage');
  const add = () => newAgent(context);
  root.replaceChildren(h('section.page', h('div.wrap',
    h('div.pg-h',
      h('div', h('p.over', label(context.project)), h('h1.h1', 'Agents')),
      h('div.act', manage && h('button.btn.pri', {type: 'button', onclick: add}, icon('plus'), 'New agent'))),
    context.agents.length
      ? h('div.ag-grid', context.agents.map((agent) => agentCard(context, agent, {full: true})),
        manage && h('button.card.agent.new', {type: 'button', onclick: add}, h('span.box', icon('plus', 'lg')), 'New agent'))
      : h('div.card', manage
        ? empty('spark', 'No agents yet', null, h('button.btn.pri', {type: 'button', onclick: add}, icon('plus'), 'New agent'))
        : empty('spark', 'No agents yet')),
  )));
  return {context};
}
