// Who is in a project, and what each role may do.

import {h, icon, time, initials, plural} from './util.js';
import {api} from './api.js';
import {app, project, refresh, person, mine, ALL, ALLOWS, ROLE_NOTES} from './state.js';
import {dialog, confirm, field, select, check, segmented, wrong, avatar, roleBadge, adminBadge, label, menu, item, toast, oops, empty} from './ui.js';

const ROLES = ['owner', 'editor', 'user', 'viewer', 'auditor'];

// summary says in a few words what a set of permissions allows.
function summary(permissions) {
  if (ALL.every((permission) => permissions.includes(permission))) return 'Everything in this project';
  const rest = permissions.filter((permission) => permission !== 'project.view' && !(permission === 'sessions.view_all' && permissions.includes('sessions.control_all')));
  return ['See the project', ...rest.map((permission) => ALLOWS[permission].toLowerCase())].join(' · ');
}

// permissionBoxes is the list of permissions with a box each. "See the
// project" is always held, and controlling every session includes seeing
// them.
function permissionBoxes(held) {
  const boxes = {};
  const sync = () => {
    const controls = boxes['sessions.control_all'].checked;
    if (controls) boxes['sessions.view_all'].checked = true;
    boxes['sessions.view_all'].disabled = controls;
  };
  const node = h('div.perms', ALL.map((permission) => {
    const row = check({checked: permission === 'project.view' || held.includes(permission), disabled: permission === 'project.view', onchange: sync}, h('b', ALLOWS[permission], ' ', h('span.tag', permission)));
    boxes[permission] = row.querySelector('input');
    return row;
  }));
  sync();
  return {node, read: () => ALL.filter((permission) => boxes[permission].checked)};
}

async function addMember(context) {
  let kind = 'user';
  const username = h('input.inp', {placeholder: 'dana@example.com', autocomplete: 'off', spellcheck: 'false', autofocus: true});
  const group = h('input.inp.mono', {placeholder: 'team-a', autocomplete: 'off', spellcheck: 'false'});
  const who = h('div');
  const drawWho = () => who.replaceChildren(kind === 'user'
    ? field('Username', username)
    : field('Group', group));
  drawWho();
  const role = select({onchange: () => { custom.node.hidden = role.firstChild.value !== 'custom'; }},
    [...ROLES.map((name) => [name, `${name}: ${ROLE_NOTES[name].toLowerCase()}`, name === 'user']), ['custom', 'Custom permissions…']]);
  const custom = permissionBoxes(['sessions.run']);
  custom.node.hidden = true;

  const added = await dialog({
    title: 'Add a member',
    action: 'Add member',
    body: [
      h('div.fld', segmented([['user', 'A person', 'user'], ['group', 'A group', 'users']], kind, (value) => { kind = value; drawWho(); }, 'lg')),
      h('div.fld', who),
      field('Role', role),
      h('div.fld', custom.node),
    ],
    submit: () => {
      const body = kind === 'user' ? {user: username.value.trim()} : {group: group.value.trim()};
      if (!body.user && !body.group) throw wrong(kind === 'user' ? 'Enter a username.' : 'Enter a group.');
      if (role.firstChild.value === 'custom') body.permissions = custom.read();
      else body.role = role.firstChild.value;
      return api.post(`/v1/projects/${context.project.id}/members`, body);
    },
  });
  if (!added) return false;
  await refresh(context, 'members');
  toast('Added ' + (added.group || added.user.display_name || added.user.username), 'ok');
  return true;
}

function nameOf(member) {
  return member.group || member.user.display_name || member.user.username;
}

async function setCustom(context, member) {
  const boxes = permissionBoxes(member.role === 'custom' ? member.permissions : context.roles.find((role) => role.name === member.role).permissions);
  const saved = await dialog({
    title: 'Permissions of ' + nameOf(member),
    action: 'Save permissions',
    body: boxes.node,
    submit: () => api.put('/v1/members/' + member.id, {permissions: boxes.read()}),
  });
  if (saved) await refresh(context, 'members');
  return !!saved;
}

export async function accessView(root, params) {
  const context = await project(params.project);
  const admin = app.me.admin;
  let filter = '';
  // The boxes of the roles that were ticked or cleared and not saved yet.
  let draft = null;

  const draw = () => {
    const {members, roles} = context;
    const again = (changed) => { if (changed) draw(); };
    draft ||= Object.fromEntries(roles.map((role) => [role.name, [...role.permissions]]));
    const permissionsOf = (member) => (member.role === 'custom' ? member.permissions : roles.find((role) => role.name === member.role)?.permissions || []);

    // ---- Your access ----
    const through = mine(context);
    const you = admin
      ? h('div.card.yours', h('span.av.lg', {style: {background: 'var(--badge)', color: 'var(--canary)'}}, icon('shield', 'lg')),
        h('div.tx', h('b', 'You are an administrator'), h('p', app.config.idp
          ? 'You can do everything in every project.'
          : 'Without sign-in, everyone is. Members and roles do not apply.')),
        adminBadge('Administrator'))
      : h('div.card.yours', avatar(person(context, app.me.id), 'lg'),
        h('div.tx', h('b', 'Your access'), h('p', summary(context.permissions))),
        h('div', {style: {display: 'flex', gap: '6px'}}, [...new Set(through.map((member) => member.role))].map(roleBadge)));

    // ---- Members ----
    const shown = members.filter((member) => !filter || [member.group, member.user?.display_name, member.user?.username, member.role].some((text) => text?.toLowerCase().includes(filter)));
    const row = (member) => {
      const isMe = member.user?.id && member.user.id === app.me.id;
      const inGroup = member.group && (app.me.groups || []).includes(member.group);
      const face = member.group ? h('span.av.grp.lg', icon('users', 'lg'))
        : member.user.id ? avatar(person(context, member.user.id), 'lg')
          : h('span.av.wait.lg', initials(member.user.username)[0].toLowerCase());
      const who = member.group ? [h('b.mono', {style: {fontSize: '13.5px'}}, member.group, inGroup && h('span.you', 'includes you')), h('small', 'Group')]
        : member.user.id ? [h('b', member.user.display_name || member.user.username, isMe && h('span.you', 'you')), h('small', member.user.username)]
          : [h('b', member.user.username), h('small', 'Not signed in yet')];
      const kind = member.group ? ['users', 'Group'] : member.user.id ? ['user', 'User'] : ['clock', 'Awaited'];
      const setRole = async (role) => {
        try {
          await api.put('/v1/members/' + member.id, {role});
          await refresh(context, 'members');
          draw();
        } catch (error) {
          oops(error);
        }
      };
      const roleCell = admin
        ? (() => {
          const button = h('button.rsel', {type: 'button', 'aria-haspopup': 'true', title: 'Change the role', onclick: () => menu(button, (close) => [
            h('small.h', 'Role of ', nameOf(member)),
            ROLES.map((name) => item({lead: roleBadge(name), text: '', small: ROLE_NOTES[name], on: member.role === name, onclick: () => setRole(name)}, close)),
            h('hr'),
            item({lead: roleBadge('custom'), text: '', small: 'Custom permissions…', on: member.role === 'custom', onclick: () => setCustom(context, member).then(again)}, close),
          ])}, roleBadge(member.role), icon('down'));
          return button;
        })()
        : roleBadge(member.role);
      const more = admin && (() => {
        const button = h('button.ib', {type: 'button', title: 'More', 'aria-haspopup': 'true', onclick: () => menu(button, (close) => [
          item({glyph: 'set', text: 'Custom permissions…', onclick: () => setCustom(context, member).then(again)}, close),
          h('hr'),
          item({glyph: 'trash', text: 'Remove from the project', danger: true, onclick: async () => {
            const gone = await confirm({
              title: `Remove ${nameOf(member)}?`,
              lead: member.group ? 'Its members lose the access that it gives them.' : 'They lose access to this project.',
              action: 'Remove member', danger: true,
              submit: () => api.delete('/v1/members/' + member.id),
            });
            if (!gone) return;
            await refresh(context, 'members');
            toast(`Removed ${nameOf(member)}`, 'ok');
            draw();
          }}, close),
        ], {align: 'right'})}, icon('dots', 'lg'));
        return button;
      })();
      return h('tr',
        h('td', h('div.who', face, h('div', who))),
        h('td', h('span.kind', icon(kind[0]), kind[1])),
        h('td', roleCell),
        h('td.can', member.role === 'custom' ? member.permissions.map((permission) => h('span.tag.b', permission)) : summary(permissionsOf(member))),
        h('td', more));
    };
    const search = h('input.inp', {placeholder: 'Find a member', value: filter, 'aria-label': 'Find a member', oninput: (event) => {
      filter = event.target.value.trim().toLowerCase();
      body.replaceChildren(...rows());
    }});
    const rows = () => {
      const list = members.filter((member) => !filter || [member.group, member.user?.display_name, member.user?.username, member.role].some((text) => text?.toLowerCase().includes(filter)));
      return list.length ? list.map(row) : [h('tr', h('td', {colspan: 5}, h('p.sub', {style: {padding: '12px 8px'}}, `No member matches “${filter}”.`)))];
    };
    const body = h('tbody', shown.length ? shown.map(row) : rows());

    // ---- Roles ----
    const changes = () => roles.filter((role) => ALL.some((permission) => draft[role.name].includes(permission) !== role.permissions.includes(permission)));
    const status = h('span');
    const discard = h('button.btn.grey', {type: 'button', onclick: () => { draft = null; draw(); }}, 'Discard');
    const save = h('button.btn.pri', {type: 'button', onclick: async () => {
      save.disabled = true;
      try {
        for (const role of changes()) await api.put('/v1/roles/' + role.name, {permissions: draft[role.name]});
        draft = null;
        await refresh(context, 'roles');
        toast('Roles saved', 'ok');
      } catch (error) {
        oops(error);
      }
      draw();
    }}, 'Save changes');
    const newest = roles.reduce((latest, role) => (!latest || role.updated_at > latest.updated_at ? role : latest), null);
    const footer = () => {
      const count = roles.reduce((total, role) => total + ALL.filter((permission) => draft[role.name].includes(permission) !== role.permissions.includes(permission)).length, 0);
      save.disabled = discard.disabled = !count;
      status.replaceChildren(count ? h('span.dirty', plural(count, 'unsaved change')) : admin ? '' : 'Only administrators can change roles.');
    };
    const cell = (role, permission) => {
      const held = draft[role.name].includes(permission);
      if (!admin) return h('td', held ? h('span.yes', {title: 'Allowed'}, icon('check', 'lg')) : h('span.no', {title: 'Not allowed'}));
      const implied = permission === 'sessions.view_all' && draft[role.name].includes('sessions.control_all');
      const box = check({checked: held, disabled: permission === 'project.view' || implied, 'aria-label': `${role.name}: ${ALLOWS[permission]}`, onchange: (event) => {
        const set = new Set(draft[role.name]);
        if (event.target.checked) set.add(permission); else set.delete(permission);
        if (set.has('sessions.control_all')) set.add('sessions.view_all');
        draft[role.name] = ALL.filter((each) => set.has(each));
        matrix.replaceChildren(...matrixRows());
        footer();
      }});
      return h('td' + (held !== role.permissions.includes(permission) ? '.chg' : ''), box);
    };
    const matrixRows = () => roles.map((role) => h('tr', h('td', roleBadge(role.name), h('small', ROLE_NOTES[role.name] || '')), ALL.map((permission) => cell(role, permission))));
    const matrix = h('tbody', matrixRows());
    footer();

    root.replaceChildren(h('section.page', h('div.wrap',
      h('div.pg-h', h('div', h('p.over', label(context.project)), h('h1.h1', 'Access'))),
      you,
      h('div.card',
        h('div.tb-h', h('h2.h2', 'Members'), h('span.cnt', members.length),
          h('div.end', members.length > 4 && h('label.inp-i', icon('search'), search),
            admin && h('button.btn.pri', {type: 'button', onclick: () => addMember(context).then(again)}, icon('plus'), 'Add member'))),
        members.length
          ? h('table.tb', h('thead', h('tr', h('th', {style: {width: '30%'}}, 'Member'), h('th', {style: {width: '13%'}}, 'Kind'), h('th', {style: {width: '17%'}}, 'Role'), h('th', 'What they may do'), h('th', {style: {width: '48px'}}))), body)
          : empty('users', 'No members yet'),
        !admin && h('div.tb-f', icon('info'), 'Only administrators can change members.')),
      h('div.card.sec',
        h('div.tb-h', h('div', h('h2.h2', 'Roles'),
          h('p.sub.sm', 'The same in every project.',
            newest?.updated_by ? [' Last changed ', time(newest.updated_at), ' by ', person(context, newest.updated_by).name, '.'] : ''))),
        h('table.tb.mx', h('thead', h('tr', h('th', 'Role'), ALL.map((permission) => h('th', h('b', ALLOWS[permission]), h('code', permission))))), matrix),
        h('div.tb-f', status, admin && h('div.end', discard, save))),
    )));
  };
  draw();
  return {context};
}
