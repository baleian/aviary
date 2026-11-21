// What the installation read from the caller's token. A caller who may not
// use the installation can still see this, to find out why.

import {h, icon} from './util.js';
import {api} from './api.js';
import {app} from './state.js';
import {avatar, adminBadge, copyButton} from './ui.js';
import {signOut} from './shell.js';

// identity is the table of what the token said.
export function identity(me) {
  const id = (value, what) => (value ? h('div.idb', h('span', value), copyButton(value, what)) : h('span.sub', 'None'));
  return h('dl.kv.wide',
    h('dt', 'User ID'), h('dd', id(me.id, 'user ID')),
    h('dt', 'Subject'), h('dd', id(me.subject, 'subject')),
    h('dt', 'Username'), h('dd', me.username || h('span.sub', 'None')),
    h('dt', 'Display name'), h('dd', me.display_name || h('span.sub', 'None')),
    h('dt', 'Groups'), h('dd.top', (me.groups || []).length ? h('div.groups', me.groups.map((group) => h('span.tag', group))) : h('span.sub', 'None')),
    h('dt', 'Administrator'), h('dd', me.admin ? 'Yes' : 'No'));
}

export async function meView(root) {
  const me = app.me = await api.get('/v1/me');
  if (!app.config.idp) {
    root.replaceChildren(h('section.page', h('div.wrap.narrow', h('div.card.acct-card',
      h('div.acct-top', {style: {border: 0, margin: 0, padding: 0}}, h('span.av.xl.anon', icon('user', 'xl')), h('div', h('h1', 'No sign-in'), h('p', 'Everyone is an administrator here.')))))));
    return {};
  }
  const who = {name: me.display_name || me.username || 'You', known: true, key: me.id};
  root.replaceChildren(h('section.page', h('div.wrap.narrow',
    h('div.card.acct-card',
      h('div.acct-top', avatar(who, 'xl'),
        h('div', h('h1', who.name, me.admin && adminBadge('Administrator')), h('p', me.username || me.subject)),
        h('button.btn.sec', {type: 'button', onclick: signOut}, icon('logout'), 'Sign out')),
      identity(me)),
  )));
  return {};
}
