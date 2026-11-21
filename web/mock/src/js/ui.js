// The parts that the views share: tiles, chips, menus, dialogs, toasts.

import {h, use, icon, initials, pick, toClipboard} from './util.js';

const layers = () => document.getElementById('layers');

// ---- People, agents, projects ---------------------------------------------

// label returns the name to show for a project or an agent: its name, or its
// slug if it has none.
export function label(record) {
  return record.name || record.slug;
}

// avatar shows a person. who is {name, known}: see person() in state.js.
export function avatar(who, size = '') {
  if (!who.known) return h('span.av.anon' + (size ? '.' + size : ''), {title: who.title || who.name}, icon('user'));
  const colour = ['', '.a2', '.a3', '.a4'][pick(who.key || who.name, 4)];
  return h('span.av' + colour + (size ? '.' + size : ''), {title: who.name}, initials(who.name));
}

export function agentTile(agent, size = '') {
  const plumage = (agent.tint ?? pick(agent.id, 6)) % 6 + 1;
  return h('span.at.t' + plumage + (size ? '.' + size : ''), {'aria-hidden': 'true'}, use('#bird'));
}

export function projectTile(project, size = '') {
  const colour = ['', '.p2', '.p3', '.p4'][(project.tint ?? pick(project.id, 4)) % 4];
  return h('span.pt' + colour + (size ? '.' + size : ''), {'aria-hidden': 'true'}, label(project)[0]);
}

export function roleBadge(role) {
  return h('span.role.r-' + role, role === 'auditor' ? icon('eye') : null, role);
}

export function adminBadge(text = 'Admin') {
  return h('span.adm', icon('shield'), text);
}

// ---- The state of a turn ----------------------------------------------------

const STATES = {
  queued: 'Queued', starting: 'Starting', running: 'Running', waiting: 'Needs approval', stopping: 'Stopping',
  completed: 'Completed', failed: 'Failed', cancelled: 'Cancelled', expired: 'Expired',
};

// status returns how a turn stands, as a key of STATES. Besides the states
// of the API it tells apart the moments of a running turn that look
// different: before its harness has the message, while it waits for a
// person, and after it was told to stop.
export function status(turn, waiting = turn.approvals?.length > 0) {
  if (turn.state !== 'running') return turn.state;
  if (turn.cancel_requested_at) return 'stopping';
  if (waiting) return 'waiting';
  return turn.started_at ? 'running' : 'starting';
}

export function chip(key, text = STATES[key]) {
  return h('span.chip.st-' + key, text);
}

// stateIcon is the small mark of a state, for lists.
export function stateIcon(key) {
  switch (key) {
    case 'starting': case 'running': case 'stopping': case 'waiting': return h('span.ring');
    case 'queued': return h('span.c-dusk', icon('clock', 'lg'));
    case 'completed': return h('span.c-ok', icon('ok', 'lg'));
    case 'failed': return h('span.c-fail', icon('fail', 'lg'));
    case 'expired': return h('span.c-grey', icon('hour', 'lg'));
    case 'ended': return h('span.c-grey', icon('lock', 'lg'));
    case 'none': return h('span.c-grey', icon('chat', 'lg'));
    default: return h('span.c-grey', icon('cancel', 'lg'));
  }
}

export function stateName(key) {
  return STATES[key] || key;
}

// ---- Toasts ---------------------------------------------------------------

export function toast(message, kind = '', ms = 3600) {
  let stack = layers().querySelector('.toasts');
  if (!stack) layers().append(stack = h('div.toasts', {role: 'status', 'aria-live': 'polite'}));
  const node = h('div.toast' + (kind ? '.' + kind : ''), icon(kind === 'bad' ? 'warn' : kind === 'ok' ? 'check' : 'info'), h('span', message));
  // Two at most: a newer one is worth more than an older one.
  while (stack.children.length > 1) stack.firstChild.remove();
  stack.append(node);
  setTimeout(() => node.remove(), ms);
}

// oops shows a failed request as a toast.
export function oops(error) {
  if (!error.status) console.error(error);
  toast(sentence(error.message), 'bad', 5200);
}

// sentence makes a message of the API a sentence: its messages begin in
// lower case and end without a full stop.
export function sentence(message) {
  const text = String(message || 'Something went wrong').trim();
  return text[0].toUpperCase() + text.slice(1) + (/[.!?…]$/.test(text) ? '' : '.');
}

export function copyButton(text, what = 'ID') {
  return h('button.ib.sm', {type: 'button', title: 'Copy the ' + what, onclick: async (event) => {
    event.stopPropagation();
    toast(await toClipboard(text) ? `Copied the ${what}` : `Could not copy the ${what}`, 'ok', 1800);
  }}, icon('copy'));
}

// ---- Menus ----------------------------------------------------------------

let closeMenu = null;

// menu opens a menu below an anchor. build(close) returns its content.
// Opening it again from the same anchor closes it.
export function menu(anchor, build, {align = 'left', cls = ''} = {}) {
  const again = closeMenu && closeMenu.anchor === anchor;
  closeMenu?.();
  if (again) return;
  const pop = h('div.pop' + (cls ? '.' + cls : ''), {role: 'menu'});
  const close = () => {
    pop.remove();
    anchor.setAttribute('aria-expanded', 'false');
    document.removeEventListener('pointerdown', outside, true);
    document.removeEventListener('keydown', key, true);
    window.removeEventListener('hashchange', close);
    window.removeEventListener('resize', close);
    if (closeMenu === close) closeMenu = null;
  };
  const outside = (event) => { if (!pop.contains(event.target) && !anchor.contains(event.target)) close(); };
  const key = (event) => {
    if (event.key === 'Escape') { event.stopPropagation(); close(); anchor.focus(); }
  };
  close.anchor = anchor;
  closeMenu = close;
  pop.append(...[build(close)].flat(Infinity).filter(Boolean));
  layers().append(pop);
  const box = anchor.getBoundingClientRect();
  const width = pop.offsetWidth, height = pop.offsetHeight;
  let left = align === 'right' ? box.right - width : box.left;
  left = Math.max(8, Math.min(left, window.innerWidth - width - 8));
  let top = box.bottom + 8;
  if (top + height > window.innerHeight - 8 && box.top - height - 8 > 8) top = box.top - height - 8;
  Object.assign(pop.style, {left: left + 'px', top: top + 'px'});
  anchor.setAttribute('aria-expanded', 'true');
  document.addEventListener('pointerdown', outside, true);
  document.addEventListener('keydown', key, true);
  window.addEventListener('hashchange', close);
  window.addEventListener('resize', close);
}

// item is a row of a menu.
export function item({text, small, glyph, lead, end, hint, on, danger, disabled, href, onclick}, close) {
  const content = [lead, glyph && icon(glyph), small ? h('span', text, h('small', small)) : h('span.nm', text), hint && h('em', hint), on && icon('check', 'end'), end];
  const cls = (on ? '.on' : '') + (danger ? '.dng' : '');
  if (href) return h('a.it' + cls, {href, role: 'menuitem', onclick: () => close?.()}, content);
  return h('button.it' + cls, {type: 'button', role: 'menuitem', disabled, onclick: () => { close?.(); onclick?.(); }}, content);
}

// ---- Dialogs --------------------------------------------------------------

// dialog opens a dialog with a form. submit, if given, runs when the form is
// sent: the dialog shows its error if it throws, and closes with its result
// otherwise. The returned promise resolves with that result, or with null if
// the dialog is dismissed.
export function dialog({title, lead, body = [], action = 'Save', danger = false, size = '', submit, cancel = 'Cancel', extra}) {
  return new Promise((resolve) => {
    const alert = h('div.alert.bad', {hidden: true, role: 'alert'}, icon('warn'), h('span'));
    const go = h('button.btn.' + (danger ? 'dng' : 'pri'), {type: 'submit'}, action);
    const no = cancel && h('button.btn.grey', {type: 'button', onclick: () => finish(null)}, cancel);
    const form = h('form', {novalidate: true},
      h('div.dlg-h', h('div', h('h2', {id: 'dlg-title'}, title), lead && h('p', lead)),
        h('button.ib', {type: 'button', title: 'Close', onclick: () => finish(null)}, icon('x', 'lg'))),
      h('div.dlg-b', alert, body),
      h('div.dlg-f', extra && h('span.left', extra), no, go));
    const node = h('dialog.dlg' + (size ? '.' + size : ''), {'aria-labelledby': 'dlg-title'}, form);
    let done = false;
    const finish = (value) => {
      if (done) return;
      done = true;
      node.close();
      node.remove();
      resolve(value);
    };
    node.addEventListener('cancel', (event) => { event.preventDefault(); finish(null); });
    node.addEventListener('pointerdown', (event) => { if (event.target === node) finish(null); });
    form.addEventListener('submit', async (event) => {
      event.preventDefault();
      if (go.disabled) return;
      alert.hidden = true;
      if (!submit) { finish(true); return; }
      const text = go.textContent;
      go.disabled = true;
      go.replaceChildren(h('span.ring'), text);
      try {
        const value = await submit(new FormData(form), form);
        finish(value === undefined ? true : value);
      } catch (error) {
        if (!error.field && !error.status) console.error(error);
        alert.lastChild.textContent = sentence(error.message);
        alert.hidden = false;
        alert.scrollIntoView({block: 'nearest'});
        go.disabled = false;
        go.replaceChildren(text);
      }
    });
    layers().append(node);
    node.showModal();
    (form.querySelector('[autofocus]') || form.querySelector('.dlg-b input, .dlg-b textarea, .dlg-b select') || go).focus();
  });
}

export function confirm({title, lead, body, action = 'Confirm', danger = false, submit}) {
  return dialog({title, lead, body, action, danger, size: 'slim', submit});
}

// wrong is an error about what a person entered, for submit() to throw.
export function wrong(message) {
  return Object.assign(new Error(message), {field: true});
}

// ---- Form fields ------------------------------------------------------------

// field wraps a control with its label and hint.
export function field(name, control, {hint, optional} = {}) {
  return h('label.fld', h('span.lab', name, optional && h('em', typeof optional === 'string' ? optional : 'optional')), control, hint && h('span.hint', hint));
}

export function select(attrs, options) {
  return h('span.sel', h('select.inp', attrs, options.map(([value, text, selected]) => h('option', {value, selected}, text))), icon('down'));
}

export function check(attrs, ...text) {
  return h('label.cb', h('input', {type: 'checkbox', ...attrs}), h('i'), text.length ? h('span', text) : null);
}

export function toggle(attrs, ...text) {
  return h('label.swt', h('input', {type: 'checkbox', ...attrs}), h('i'), h('span', text));
}

// options is a choice among a few cards. choices are [value, title, text,
// body], where body shows below the card while it is chosen.
export function options(name, chosen, choices, onchange) {
  const root = h('div.opts');
  const draw = () => {
    for (const card of root.querySelectorAll('.opt')) {
      const body = card.nextElementSibling;
      if (body?.classList.contains('opt-body')) body.hidden = !card.querySelector('input').checked;
    }
  };
  for (const [value, title, text, body] of choices) {
    root.append(h('label.opt', h('input', {type: 'radio', name, value, checked: value === chosen, onchange: () => { draw(); onchange?.(value); }}),
      h('span.dot'), h('span', h('b', title), text && h('small', text))));
    if (body) root.append(h('div.opt-body', body));
  }
  draw();
  return root;
}

export function segmented(choices, chosen, onchange, size = '') {
  const root = h('span.seg' + (size ? '.' + size : ''), {role: 'group'});
  const draw = (value) => {
    root.replaceChildren(...choices.map(([key, text, glyph]) =>
      h('button', {type: 'button', 'aria-pressed': String(key === value), onclick: () => { draw(key); onchange(key); }}, glyph && icon(glyph), text)));
  };
  draw(chosen);
  return root;
}

// ---- Page furniture -----------------------------------------------------------

export function empty(glyph, title, text, action) {
  return h('div.empty', h('span.ic', icon(glyph, 'xl')), h('b', title), text && h('p', text), action);
}

export function note(kind, glyph, ...content) {
  return h('div.alert' + (kind ? '.' + kind : ''), icon(glyph), h('span', content));
}

export function loading(lines = 3) {
  return h('div', {style: {display: 'grid', gap: '12px', padding: '8px 0'}, 'aria-busy': 'true'},
    Array.from({length: lines}, (_, index) => h('span.skel', {style: {height: '18px', width: `${86 - index * 17}%`}})));
}

// blank is a page that says one thing.
export function blank({glyph, tone = '', title, text, actions, more, full = false}) {
  return h('section.blank' + (full ? '.full' : ''), h('div.box',
    h('span.ic' + (tone ? '.' + tone : ''), icon(glyph, 'xl')), h('h1', title), text && h('p', text), more, actions && h('div.acts', actions)));
}
