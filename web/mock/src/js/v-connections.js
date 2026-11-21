// The connections of a project: the model providers that its agents use, and
// the secret stores that may hold their keys.

import {h, icon, time, code} from './util.js';
import {api} from './api.js';
import {project, refresh, can} from './state.js';
import {dialog, field, select, options, wrong, agentTile, label, empty, toast} from './ui.js';
import {keyPill} from './v-overview.js';

const secret = (attrs) => h('input.inp.mono', {type: 'password', autocomplete: 'new-password', spellcheck: 'false', ...attrs});

// editProvider creates a provider, or replaces one: its name, URL and key
// together, so the key is entered again.
async function editProvider(context, existing) {
  const stores = context.stores;
  const at = existing?.api_key?.store ? existing.api_key : null;
  const name = h('input.inp', {name: 'name', value: existing?.name || '', placeholder: 'Company gateway', autocomplete: 'off', autofocus: true});
  const url = h('input.inp.mono', {name: 'url', value: existing?.url || '', placeholder: 'https://api.example.com/v1', autocomplete: 'off', spellcheck: 'false'});
  const value = secret({name: 'value', placeholder: existing?.api_key?.stored ? 'Enter the key again' : 'API key'});
  const store = select({name: 'store'}, stores.map((each) => [each.id, each.name, at?.store === each.id]));
  const path = h('input.inp.mono', {name: 'path', value: at?.path || '', placeholder: 'team/model', autocomplete: 'off', spellcheck: 'false'});
  const key = h('input.inp.mono', {name: 'key', value: at?.key || '', placeholder: 'api_key', autocomplete: 'off', spellcheck: 'false'});
  let how = !existing ? 'value' : at ? 'store' : existing.api_key ? 'value' : 'none';

  const saved = await dialog({
    title: existing ? 'Edit ' + existing.name : 'Add a model provider',
    size: 'wide',
    action: existing ? 'Save provider' : 'Add provider',
    body: [
      h('div.fld-row', field('Name', name), field('URL', url)),
      h('div.fld', h('span.lab', 'API key'),
        options('how', how, [
          ['value', 'Store the key in Aviary', '', value],
          ['store', 'Use a key from a secret store', '',
            stores.length ? [h('div.fld-row', field('Secret store', store), field('Path', path)), field('Key', key)]
              : h('p.sub.sm', 'This project has no secret store yet.')],
          ['none', 'No key', ''],
        ], (chosen) => { how = chosen; })),
    ],
    submit: () => {
      const body = {name: name.value.trim(), url: url.value.trim()};
      if (!body.name) throw wrong('Enter a name.');
      if (!body.url) throw wrong('Enter the URL of the model endpoint.');
      if (how === 'value') {
        if (!value.value) throw wrong(existing ? 'Enter the key again.' : 'Enter the key.');
        body.api_key = {value: value.value};
      } else if (how === 'store') {
        if (!stores.length) throw wrong('Register a secret store first.');
        if (!path.value.trim() || !key.value.trim()) throw wrong('Enter the path and the key of the secret.');
        body.api_key = {store: store.firstChild.value, path: path.value.trim(), key: key.value.trim()};
      }
      return existing ? api.put('/v1/providers/' + existing.id, body) : api.post(`/v1/projects/${context.project.id}/providers`, body);
    },
  });
  if (!saved) return false;
  await refresh(context, 'providers');
  toast(existing ? `Saved ${saved.name}` : `Added ${saved.name}`, 'ok');
  return true;
}

// editStore registers a secret store, or replaces one whole, so the
// credential is entered again.
async function editStore(context, existing) {
  const settings = existing?.settings || {};
  const known = !existing || existing.kind === 'kv-v2';
  const name = h('input.inp', {name: 'name', value: existing?.name || '', placeholder: 'Platform secrets', autocomplete: 'off', autofocus: true});
  const kind = select({name: 'kind', onchange: () => draw()}, [['kv-v2', 'kv-v2', known], ['', 'Another kind…', !known]]);
  const address = h('input.inp.mono', {value: settings.address || '', placeholder: 'https://secrets.example.com:8200', autocomplete: 'off', spellcheck: 'false'});
  const mount = h('input.inp.mono', {value: settings.mount || '', placeholder: 'secret', autocomplete: 'off', spellcheck: 'false'});
  const role = h('input.inp.mono', {value: settings.role_id || '', autocomplete: 'off', spellcheck: 'false'});
  const other = h('input.inp.mono', {value: known ? '' : existing.kind, autocomplete: 'off', spellcheck: 'false'});
  const raw = h('textarea.inp.mono', {rows: 5, spellcheck: 'false', placeholder: '{\n  "address": "https://…"\n}'}, known ? '' : JSON.stringify(settings, null, 2));
  const credential = secret({placeholder: existing ? 'Enter it again' : ''});
  const forKind = h('div');
  const draw = () => {
    const builtin = kind.firstChild.value === 'kv-v2';
    forKind.replaceChildren(...(builtin ? [
      h('div.fld-row', field('Address', address), field('Mount', mount)),
      h('div.fld-row', field('Role ID', role), field('Secret ID', credential)),
    ] : [
      field('Kind', other),
      field('Settings', raw, {optional: 'JSON'}),
      field('Credential', credential),
    ]));
  };
  draw();

  const saved = await dialog({
    title: existing ? 'Edit ' + existing.name : 'Register a secret store',
    size: 'wide',
    action: existing ? 'Save store' : 'Register store',
    body: [
      h('div.fld-row', field('Name', name), field('Kind', kind)),
      h('div.fld', forKind),
    ],
    submit: () => {
      const builtin = kind.firstChild.value === 'kv-v2';
      const body = {name: name.value.trim(), kind: builtin ? 'kv-v2' : other.value.trim(), credential: {value: credential.value}};
      if (!body.name) throw wrong('Enter a name.');
      if (builtin) {
        body.settings = {address: address.value.trim(), mount: mount.value.trim(), role_id: role.value.trim()};
      } else {
        if (!body.kind) throw wrong('Enter the kind of the store.');
        try {
          body.settings = JSON.parse(raw.value || '{}');
        } catch (error) {
          throw wrong('The settings are not valid JSON.');
        }
      }
      if (!credential.value) throw wrong(existing ? 'Enter the credential again.' : 'Enter the credential.');
      return existing ? api.put('/v1/secret-stores/' + existing.id, body) : api.post(`/v1/projects/${context.project.id}/secret-stores`, body);
    },
  });
  if (!saved) return false;
  await refresh(context, 'stores');
  toast(existing ? `Saved ${saved.name}` : `Registered ${saved.name}`, 'ok');
  return true;
}

export async function connectionsView(root, params) {
  const context = await project(params.project);
  const manage = can(context, 'connections.manage');
  const draw = () => {
    const {providers, stores, agents} = context;
    const again = (changed) => { if (changed) draw(); };
    const providerRow = (provider) => {
      const users = agents.filter((agent) => agent.model.provider === provider.id);
      return h('li',
        h('span.ic', icon('spark', 'lg')),
        h('div.tx', h('b', provider.name), code(provider.url),
          provider.api_key?.store && code(`${provider.api_key.path} → ${provider.api_key.key}`)),
        h('div.used', users.length ? ['Used by', users.slice(0, 3).map((agent) => h('span.usr', agentTile(agent, 'xs'), label(agent))), users.length > 3 && `and ${users.length - 3} more`] : 'Not used yet'),
        h('div.meta', keyPill(context, provider), h('small', 'Updated ', time(provider.updated_at))),
        manage && h('button.btn.sec.sm', {type: 'button', onclick: () => editProvider(context, provider).then(again)}, 'Edit'));
    };
    const storeRow = (store) => {
      const users = providers.filter((provider) => provider.api_key?.store === store.id);
      const set = store.settings || {};
      return h('li',
        h('span.ic', icon('db', 'lg')),
        h('div.tx', h('b', store.name), code(store.kind === 'kv-v2' ? `${set.address} · ${set.mount}` : JSON.stringify(set))),
        h('div.used', users.length ? ['Holds the key of ', users.map((provider) => provider.name).join(', ')] : 'Not used yet'),
        h('div.meta', h('span', {style: {display: 'flex', gap: '6px'}}, h('span.tag', store.kind), h('span.pill.g', icon('lock'), 'Credential stored')),
          h('small', 'Updated ', time(store.updated_at))),
        manage && h('button.btn.sec.sm', {type: 'button', onclick: () => editStore(context, store).then(again)}, 'Edit'));
    };
    const addProvider = () => editProvider(context).then(again);
    const addStore = () => editStore(context).then(again);

    root.replaceChildren(h('section.page', h('div.wrap',
      h('div.pg-h', h('div', h('p.over', label(context.project)), h('h1.h1', 'Connections'))),
      h('div.card',
        h('div.tb-h', h('h2.h2', 'Model providers', h('span.cnt', providers.length)),
          h('div.end', manage && h('button.btn.pri', {type: 'button', onclick: addProvider}, icon('plus'), 'Add provider'))),
        providers.length ? h('ul.clist', providers.map(providerRow)) : empty('spark', 'No model providers yet')),
      h('div.card.sec',
        h('div.tb-h', h('h2.h2', 'Secret stores', h('span.cnt', stores.length)),
          h('div.end', manage && h('button.btn.sec', {type: 'button', onclick: addStore}, icon('plus'), 'Register store'))),
        stores.length ? h('ul.clist', stores.map(storeRow)) : empty('db', 'No secret stores yet')),
    )));
  };
  draw();
  return {context};
}
