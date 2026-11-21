// The brand sheet: the logo, the colours of both themes, the type and the
// parts. It belongs to the mock, not to the product.

import {h, icon, use} from './util.js';
import {chip, roleBadge, check, toggle, select} from './ui.js';
import {codeBlock} from './markdown.js';

const COLOURS = [
  ['page', 'Page'], ['surface', 'Surface'], ['surface-2', 'Inset'], ['surface-3', 'Neutral fill'], ['line', 'Line'], ['ink', 'Ink'], ['slate', 'Slate'],
  ['king', 'Kingfisher'], ['link', 'Link'], ['k100', 'Sky'], ['canary', 'Canary'], ['c100', 'Canary tint'],
  ['green', 'Success'], ['coral', 'Failure'], ['dusk', 'Queued'], ['night', 'Night'],
];

// sizes shows a symbol at the sizes at which it is used, each with its size.
function sizes(symbol) {
  return [64, 32, 24, 16].map((size) => h('span', use(symbol, {width: size, height: size}), size));
}

function palette(name) {
  const panel = h('div', {'data-theme': name, style: {background: 'var(--page)', color: 'var(--ink)'}},
    h('p.over', {style: {marginBottom: '12px'}}, name === 'dark' ? 'Dark' : 'Light'),
    h('div.sws', {style: {gridTemplateColumns: 'repeat(4, minmax(0, 1fr))'}}, COLOURS.map(([token, text]) =>
      h('div.swc', h('div', {style: {background: `var(--${token})`, boxShadow: 'inset 0 -1px 0 var(--line)'}}), h('b', text), h('code', {dataset: {token}}, '--' + token)))));
  // The values are read once the panel is in the page.
  requestAnimationFrame(() => {
    const style = getComputedStyle(panel);
    for (const node of panel.querySelectorAll('code[data-token]')) node.textContent = style.getPropertyValue('--' + node.dataset.token).trim().toUpperCase();
  });
  return panel;
}

// kit is one of each part, to see a theme at a glance.
function kit(name) {
  return h('div', {'data-theme': name, style: {background: 'var(--page)', color: 'var(--ink)', display: 'grid', gap: '14px'}},
    h('div.row', {style: {display: 'flex', flexWrap: 'wrap', gap: '8px', alignItems: 'center'}},
      h('button.btn.pri', {type: 'button'}, icon('plus'), 'New agent'), h('button.btn.sec', {type: 'button'}, 'Always allow'),
      h('button.btn.quiet', {type: 'button'}, 'See all'), h('button.btn.dq', {type: 'button'}, 'Reject'), h('button.btn.dng', {type: 'button'}, 'End session'),
      h('button.btn.pri', {type: 'button', disabled: true}, 'Save changes')),
    h('div', {style: {display: 'flex', flexWrap: 'wrap', gap: '6px'}},
      ['queued', 'starting', 'running', 'waiting', 'stopping', 'completed', 'failed', 'cancelled', 'expired'].map((key) => chip(key))),
    h('div', {style: {display: 'flex', flexWrap: 'wrap', gap: '6px', alignItems: 'center'}},
      ['owner', 'editor', 'user', 'viewer', 'auditor', 'custom'].map(roleBadge),
      h('span.av', 'AK'), h('span.av.a2', 'BL'), h('span.av.a3', 'CP'), h('span.av.a4', 'EC'), h('span.av.grp', icon('users')), h('span.av.wait', 'd'),
      [1, 2, 3, 4, 5, 6].map((n) => h('span.at.sm.t' + n, use('#bird')))),
    h('div.card', {style: {padding: '14px 16px', display: 'grid', gap: '12px'}},
      h('div', {style: {display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '12px'}}, h('input.inp', {value: 'Backend engineer', 'aria-label': 'Example of a text field'}), select({'aria-label': 'Example of a select'}, [['a', 'Company gateway']])),
      h('div', {style: {display: 'flex', gap: '20px', flexWrap: 'wrap'}}, check({checked: true}, 'sessions.run'), check({}, 'sessions.view_all'), toggle({checked: true}, 'Ask before it acts')),
      codeBlock('--- a/src/webhooks/sender.ts\n+++ b/src/webhooks/sender.ts\n@@ -18,3 +18,3 @@\n-  await post(url, body)\n+  await withRetry(() => post(url, body), { attempts: 5, base: 200 })', 'diff')),
    h('div', {style: {display: 'flex', justifyContent: 'flex-end'}}, h('div.bub-u', 'Great. Push it to a branch named retry-backoff.')),
    h('div.bub-a', {style: {justifySelf: 'start'}}, 'I will create the branch and push it.'),
    h('div.appr',
      h('div.appr-h', h('span.appr-i', icon('shield', 'lg')), h('div', h('h3', 'May I run this command?'), h('p', 'Backend engineer asks before it acts. The turn waits for your answer.'))),
      h('div.cmd', h('i', '$'), h('span', 'git push origin retry-backoff'), h('span.tag', 'execute')),
      h('div.appr-a', h('button.btn.pri', {type: 'button'}, 'Allow once'), h('button.btn.sec', {type: 'button'}, 'Always allow'), h('button.btn.dq', {type: 'button'}, 'Reject'))));
}

export function brandView(root) {
  root.replaceChildren(h('section.brand', h('div.wrap',
    h('p.over', 'Aviary brand sheet'),
    h('h1.display', 'Room to fly.', h('br'), h('em', 'One guarded door.')),
    h('p.lead', 'Aviary is a glasshouse for coding agents: bright, open and easy to see into. Agents work freely inside, every session in a sandbox of its own, and nothing leaves except through the door you keep.'),

    h('div.b-sec',
      h('header', h('h2', 'Logo')),
      h('div.lts',
        h('div.lt', h('small', 'On light'), use('#logo'), h('span.wm', 'Aviary')),
        h('div.lt.dk', h('small', 'On dark'), use('#logo'), h('span.wm', 'Aviary'))),
      h('div.sizes',
        h('div.sz', h('small', 'App icon'), h('div', sizes('#logo'))),
        h('div.sz', h('small', 'One colour'), h('div', sizes('#mark'))),
        h('div.sz', h('small', 'Two colours'), h('div', {style: {'--mk': '#0B6FE0', '--dot': '#FFC53D'}}, sizes('#mark'))),
        h('div.sz.dk', h('small', 'Two colours, on dark'), h('div', {style: {'--dot': '#FFC53D'}}, sizes('#mark')))),
      h('p.say', h('b', 'What it depicts. '), 'The letter A as a rounded chevron, and a Canary wing across it. The wing comes from behind one leg and breaks through the other on its way out: a bird in flight, and the one door.')),

    h('div.b-sec',
      h('header', h('h2', 'Colour'), h('p', 'Kingfisher and Canary: two birds, one cool and one warm. Canary is kept for the moments that need a person. Each theme sets the same tokens.')),
      h('div.pair', palette('light'), palette('dark'))),

    h('div.b-sec',
      h('header', h('h2', 'Typography'), h('p', 'Plus Jakarta Sans for everything, JetBrains Mono for code and identifiers, Pretendard for Korean.')),
      h('div.card', {style: {padding: '24px 28px'}},
        h('p.type-big', 'Agents in ', h('em', 'daylight')),
        h('p', {style: {display: 'flex', gap: '20px', flexWrap: 'wrap', margin: '14px 0', fontSize: '20px', lineHeight: '28px'}},
          [500, 600, 700, 800].map((weight) => h('span', {style: {fontWeight: weight}}, ['Medium', 'SemiBold', 'Bold', 'ExtraBold'][weight / 100 - 5] + ' ' + weight))),
        h('p', {style: {fontSize: '15px', lineHeight: '24px', color: 'var(--ink2)', maxWidth: '720px'}},
          'Every message to an agent is a turn. The session runs its turns in a sandbox of its own, and keeps the conversation and the files between them.'),
        h('p', {style: {fontSize: '15px', lineHeight: '24px', color: 'var(--ink2)', maxWidth: '720px', marginTop: '8px'}},
          '에이전트에게 보내는 메시지 하나가 턴 하나입니다. 세션은 자기만의 샌드박스에서 턴을 돌리고, 대화와 파일을 턴 사이에 그대로 둡니다.'),
        h('p.mono', {style: {marginTop: '14px', color: 'var(--ink2)', fontSize: '13px', lineHeight: '22px'}},
          '01a1eee7-1eb4-7016-89ad-8916bda7b5fd', h('br'), 'await withRetry(() => post(url, body), { attempts: 5, base: 200 })', h('br'), 'project.view  sessions.run  0123456789  kv-v2'))),

    h('div.b-sec',
      h('header', h('h2', 'Parts, in both themes'), h('p', 'The same parts on the light and on the dark page.')),
      h('div.pair', kit('light'), kit('dark'))),
  )));
  return {};
}
