// The bar of the mock, at the top of the page, and its panel of requests at
// the bottom. From here one signs in as someone else, chooses what the next
// turn does, and sees what the console asks of the API. Not part of the
// product.

import {h, clock, prefs} from './util.js';
import {mock, accounts, settings, setIdentityProvider, elsewhere, request} from './backend.js';
import {SCENARIOS} from './scripts.js';
import {entries, logged, clear} from './log.js';
import {signIn, signedInAs, cutStreams, network} from './api.js';
import {forget} from './state.js';
import {link, resolve} from './router.js';
import {toast} from './ui.js';

const two = (n) => String(n).padStart(2, '0');
const stamp = (at) => { const date = new Date(at); return `${clock(date)}:${two(date.getSeconds())}`; };

// mountBar draws the bar. again() is called when the caller has changed, so
// that the console loads everything anew.
export function mountBar(again) {
  const bar = document.getElementById('mockbar');
  const panel = document.getElementById('apilog');
  bar.className = 'mock';

  const who = h('select', {title: 'Whose token the console sends', onchange: () => {
    const value = who.value;
    setIdentityProvider(value !== '-');
    signIn(value === '-' || value === '' ? '' : value);
    forget();
    again();
  }});
  const drawWho = () => {
    const current = !settings().idp ? '-' : signedInAs();
    who.replaceChildren(
      h('optgroup', {label: 'Signed in as'}, accounts().map((account) => h('option', {value: account.token, selected: account.token === current}, `${account.display_name} · ${account.note}`))),
      h('optgroup', {label: 'Otherwise'},
        h('option', {value: '', selected: current === ''}, 'Signed out'),
        h('option', {value: '-', selected: current === '-'}, 'No identity provider: everyone is an administrator')));
  };

  const next = h('select', {title: 'What the next turn does. It goes back to Auto once it is used.', onchange: () => { mock.scenario = next.value; }},
    SCENARIOS.map(([value, text]) => h('option', {value}, text)));
  const speed = h('select', {title: 'How fast the agents of the mock work', onchange: () => {
    mock.speed = Number(speed.value);
    network.delay = mock.speed === 0 ? [0, 0] : [60, 170];
    prefs.set('mock.speed', speed.value);
  }}, [['1', '1×'], ['0.4', '2.5×'], ['0.12', '8×'], ['0', 'At once']].map(([value, text]) => h('option', {value}, text)));
  speed.value = prefs.get('mock.speed', '1');
  mock.speed = Number(speed.value);
  if (mock.speed === 0) network.delay = [0, 0];

  const count = h('span.n', '0');
  const toggle = h('button', {type: 'button', 'aria-pressed': 'false', title: 'The requests that the console sends to the API', onclick: () => show(panel.hidden)}, 'API', count);
  const show = (open) => {
    panel.hidden = !open;
    toggle.setAttribute('aria-pressed', String(open));
    document.documentElement.style.setProperty('--mock-bottom', open ? '248px' : '0px');
    prefs.set('mock.log', open ? '1' : null);
    if (open) drawLog(true);
  };

  bar.replaceChildren(
    h('b', 'Aviary console · mock'),
    h('label', 'View as', who),
    h('label', 'Next turn', next),
    h('label.opt2', 'Speed', speed),
    h('span.sp'),
    h('button.opt2', {type: 'button', title: 'Another member of the project sends a message, as from another screen', onclick: () => {
      const params = resolve()?.params || {};
      const sent = params.project ? elsewhere(signedInAs(), params.project, params.agent) : null;
      toast(sent ? `${sent.name} sent a message in “${sent.title}”.` : 'Nobody else can run a session here. Open a project with other members.');
    }}, 'Other user'),
    h('button.opt2', {type: 'button', title: 'Break the open event streams, as a network would. Each connects again.', onclick: () => {
      const cut = cutStreams();
      toast(cut ? `Cut ${cut} event stream${cut > 1 ? 's' : ''}. Each connected again.` : 'No event stream is open.');
    }}, 'Cut stream'),
    h('a.b', {href: link('/brand')}, 'Brand'),
    toggle);

  // ---- The panel of requests ----
  const rows = h('div.al-b');
  const key = (colour, text, title) => h('span.key', {title}, h('i', {style: {background: colour}}), text);
  panel.replaceChildren(
    h('div.al-h', h('b', 'Requests to the API'),
      key('#4A2F6B', 'proposed', 'A request, or a field of an answer, that the API does not have yet.'),
      key('#5A4311', 'planned', 'An event that the API does not send yet.'),
      key('#26323F', 'workaround', 'A request that the console sends only because the API has no better one.'),
      h('span.sp'),
      h('button', {type: 'button', onclick: clear}, 'Clear'),
      h('button', {type: 'button', onclick: () => show(false)}, 'Close')),
    rows);
  let drawn = 0;
  let first = null;
  const line = (entry) => {
    const stream = entry.method === 'SSE';
    const flag = entry.flag === 'proposed' ? h('span.flag', 'proposed') : entry.flag === 'planned' ? h('span.flag.plan', 'planned')
      : entry.flag.startsWith('field ') ? h('span.flag.fld', 'proposed ' + entry.flag)
        : entry.note === 'workaround' ? h('span.flag.work', 'workaround') : null;
    const note = stream ? ` · ${entry.events} event${entry.events === 1 ? '' : 's'}${entry.last ? ', last ' + entry.last : ''}${entry.closed ? '' : ' · open'}${entry.note ? ' · ' + entry.note : ''}` : '';
    const status = entry.status || '…';
    return h('div.al-r', h('span.t', stamp(entry.at)),
      h('span.m' + (stream ? '.s' : entry.method === 'GET' ? '' : '.w'), stream ? 'GET ⇄' : entry.method),
      h('span.p', entry.path, note && h('small', note), flag),
      h('span.c' + (entry.status >= 500 ? '.e5' : entry.status >= 400 ? '.e4' : ''), status),
      h('span.d', stream ? '' : entry.ms + ' ms'));
  };
  function drawLog(force) {
    count.textContent = entries.length;
    count.classList.toggle('hot', entries.some((entry) => entry.flag));
    if (panel.hidden && !force) return;
    const pinned = rows.scrollHeight - rows.scrollTop - rows.clientHeight < 40;
    // Entries change after they are added: a request gets its status, a
    // stream counts its events. The last few are drawn again.
    // The oldest entries are dropped once the list is long: then all is drawn anew.
    if (entries[0] !== first) {
      first = entries[0];
      drawn = 0;
    }
    const keep = Math.max(0, Math.min(drawn, entries.length) - 60);
    while (rows.children.length > keep) rows.lastChild.remove();
    for (let i = keep; i < entries.length; i++) rows.append(line(entries[i]));
    drawn = entries.length;
    if (pinned || force) rows.scrollTop = rows.scrollHeight;
  }
  logged.on(() => drawLog(false));

  // For the scripts that check the mock: what the console has asked of the
  // API, and the API itself, to ask it as another caller.
  window.aviaryMock = {requests: entries, mock, request};

  mock.changed.on(() => { next.value = mock.scenario; drawWho(); });
  drawWho();
  show(prefs.get('mock.log') === '1');
  return {refresh: drawWho};
}
