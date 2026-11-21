// The sign-in screen. Aviary does not sign users in: the console sends the
// browser to the identity provider of the installation and gets a token
// back. In the mock, a dialog stands in for the provider's page.

import {h, icon, use, initials} from './util.js';
import {signIn} from './api.js';
import {accounts} from './backend.js';
import {forget, brand} from './state.js';
import {go} from './router.js';

const ART = `
<svg class="si-art" viewBox="0 0 600 400" aria-hidden="true">
  <clipPath id="dclip"><path d="M20 400V300A280 280 0 0 1 580 300V400Z"/></clipPath>
  <path d="M20 400V300A280 280 0 0 1 580 300V400Z" fill="url(#glass)"/>
  <g clip-path="url(#dclip)" class="beam"><path d="M60 400L290 0h64L124 400Z" opacity=".42"/><path d="M196 400L426 0h22L218 400Z" opacity=".3"/></g>
  <use href="#dome" class="ribs"/>
  <g class="trails">
    <path d="M84 322C94 274 116 242 150 224"/>
    <path d="M70 200C82 170 100 146 123 131"/>
    <path d="M416 356C436 350 450 342 463 331"/>
    <path d="M430 268C474 240 504 190 521 126S548 76 571 51"/>
  </g>
  <use href="#wing" x="150" y="205" width="34" height="17" class="near" transform="rotate(-8 167 213)"/>
  <use href="#wing" x="122" y="116" width="24" height="12" class="far" transform="rotate(-14 134 122)"/>
  <use href="#wing" x="462" y="318" width="22" height="11" class="far" transform="rotate(-20 473 323)"/>
  <circle cx="520.5" cy="128" r="5.5" class="door"/>
  <use href="#wing" x="566" y="20" width="44" height="22" fill="#FFC53D" transform="rotate(-24 588 31)"/>
</svg>`;

// provider opens the stand-in for the sign-in page of the identity provider,
// and resolves with the token of the chosen account.
function provider() {
  return new Promise((resolve) => {
    const choose = (token) => { node.close(); node.remove(); resolve(token); };
    const node = h('dialog.dlg.idp',
      h('div.idp-h', h('small', 'Example identity provider'), h('h2', 'Choose an account'), h('p', 'to continue to Aviary')),
      h('div.idp-l', accounts().map((account) => h('button', {type: 'button', onclick: () => choose(account.token)},
        h('span.av.grp', initials(account.display_name)),
        h('span', h('b', account.display_name), h('small', account.username, ' · ', account.note))))),
      h('div.idp-f', 'The mock’s stand-in for the sign-in page of your organization.'));
    node.addEventListener('cancel', (event) => { event.preventDefault(); choose(null); });
    node.addEventListener('pointerdown', (event) => { if (event.target === node) choose(null); });
    document.getElementById('layers').append(node);
    node.showModal();
  });
}

export function signinView(root) {
  const art = h('div');
  art.innerHTML = ART;
  const enter = async () => {
    const token = await provider();
    if (!token) return;
    signIn(token);
    forget();
    go('/');
  };
  root.replaceChildren(h('section.signin',
    art.firstElementChild,
    h('div.si-card',
      h('div.si-logo', use(brand.logo()), 'Aviary'),
      h('p.si-tag', 'Room to fly. One guarded door.'),
      h('button.btn.pri.lg.wide', {type: 'button', onclick: enter, autofocus: true}, 'Continue with your organization', icon('arrow', 'lg'))),
  ));
}
