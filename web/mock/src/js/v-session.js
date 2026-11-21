// The workspace of an agent: its sessions on the left, a conversation in the
// middle, and the details of the session on the right.

import {h, icon, use, time, num, code, clip, prefs, toClipboard, plural} from './util.js';
import {api} from './api.js';
import {app, project, can, person, sessionAccess, controls, NotFound} from './state.js';
import {link, to, go, path} from './router.js';
import {agentTile, label, chip, avatar, stateIcon, stateName, toast, oops, sentence, menu, item, confirm, wrong, copyButton, segmented, empty, loading, blank} from './ui.js';
import {standing, standingText, endedText, title, suggest, sandboxBadge, sandboxMark} from './sessions.js';
import {feed} from './feed.js';
import {TurnView} from './turnview.js';
import {providerOf, agentMenu} from './v-agents.js';

// The page size of the lists, which is also the API's default.
const PAGE = 20;

class Workspace {
  constructor(root, context, agent, env) {
    Object.assign(this, {root, context, agent, env});
    this.rows = [];
    this.filter = 'all';
    this.current = null;
    this.opening = 0;
    this.details = prefs.get('details', '1') === '1';
    this.readsAll = app.config.idp && (app.me.admin || can(context, 'sessions.view_all'));
    this.runs = can(context, 'sessions.run');
    this.build();
  }

  // ---- The frame ------------------------------------------------------------

  build() {
    const {context, agent} = this;
    const provider = providerOf(context, agent);
    this.list = h('nav.sl', {'aria-label': 'Sessions'});
    this.listMore = h('div.sl-more');
    this.centre = h('main.conv');
    this.side = h('aside.pane.details');
    const start = h('a.btn.pri.wide', {href: link(to.agent(context.project, agent))}, icon('plus'), 'New session');
    this.frame = h('section.sess' + (this.details ? '' : '.no-details'),
      h('aside.pane.list',
        h('div.l-h',
          h('div.l-top',
            h('a.l-ag', {href: link(to.agents(context.project)), title: 'All agents of ' + label(context.project)},
              agentTile(agent), h('div', h('b', label(agent)), h('small', agent.harness, ' · ', agent.model.name))),
            agentMenu(context, agent, () => go(path()), 'sm')),
          this.runs ? start : null),
        h('div.pane-s',
          h('div.l-t', 'Sessions', this.readsAll && segmented([['all', 'All'], ['mine', 'Mine']], this.filter, (value) => { this.filter = value; this.drawList(); })),
          this.list, this.listMore)),
      this.centre, this.side);
    this.side.hidden = !this.details;
    this.root.replaceChildren(this.frame);
    this.list.replaceChildren(h('div', {style: {padding: '6px 10px'}}, loading(4)));
    this.loadList().catch(oops);
    this.provider = provider;
    this.unwatch = [
      feed.sessions.on((session) => this.took(session)),
      feed.stale.on(() => this.reread().catch(() => {})),
    ];
  }

  // close stops what the workspace does on its own.
  close() {
    this.leave();
    for (const off of this.unwatch) off();
  }

  // took takes a session as the stream of changes reports it.
  took(session) {
    if (session.agent_id !== this.agent.id) return;
    this.place(session);
    const current = this.current;
    if (current?.session?.id !== session.id) return;
    const ended = current.session.ended_at;
    current.session = session;
    this.drawHeader();
    if (session.ended_at !== ended) this.drawComposer();
    this.drawDetails();
    this.joined(current).catch(oops);
  }

  // place shows a session in the list as it is now. A session that the list
  // does not have is added if it is newer than all of them: an older one
  // comes with its page.
  place(session) {
    const row = this.rows.find((other) => other.session.id === session.id);
    if (row) row.session = session;
    else if (this.loaded && (this.listDone || !this.rows.length || session.id > this.rows[0].session.id)) this.rows.unshift({session});
    else return;
    this.rows.sort((a, b) => (a.session.id < b.session.id ? 1 : -1));
    this.drawList();
  }

  // read reads a page of the agent's sessions. A session that changes while
  // the page is read is returned as the stream of changes reports it, which
  // is newer, and so is one that is created meanwhile.
  async read(query) {
    await feed.ready();
    const changed = new Map();
    const off = feed.sessions.on((session) => { if (session.agent_id === this.agent.id) changed.set(session.id, session); });
    try {
      const {sessions} = await api.get(`/v1/agents/${this.agent.id}/sessions?${query}`);
      const newest = sessions.length ? sessions[0].id : '';
      const created = [...changed.values()].filter((session) => session.id > newest && !query.includes('before='));
      const page = [...created, ...sessions.map((session) => changed.get(session.id) || session)];
      for (const session of page) feed.note(session);
      return {page, full: sessions.length};
    } finally {
      off();
    }
  }

  // reread reads again what the list shows, which may be out of date.
  async reread() {
    const {page} = await this.read(`limit=${Math.min(Math.max(this.rows.length, PAGE), 100)}`);
    for (const session of page) this.took(session);
  }

  // joined shows the newest turn of the session in view if the page does not
  // have it yet: a message that someone else sent, or the caller from another
  // window.
  async joined(current) {
    const last = current.session.last_turn;
    const newest = current.views[current.views.length - 1];
    if (!last || this.sending || current.joining || (newest && newest.turn.seq >= last.seq)) return;
    current.joining = true;
    try {
      const record = await api.get('/v1/turns/' + last.id);
      if (this.current !== current || current.views.some((view) => view.turn.id === record.id)) return;
      const view = new TurnView(record, this.turnEnv());
      if (!current.views.length) this.thread.replaceChildren(this.olderBox);
      current.views.push(view);
      this.thread.append(view.el);
      if (this.stick) this.toBottom();
      if (!record.ended_at) this.follow(view);
      this.afterDraw(view);
      this.drawDetails();
    } finally {
      current.joining = false;
    }
  }

  toggleDetails() {
    this.details = !this.details;
    prefs.set('details', this.details ? '1' : '0');
    this.frame.classList.toggle('no-details', !this.details);
    this.side.hidden = !this.details;
    this.drawHeader();
  }

  // ---- The list of sessions -------------------------------------------------

  async loadList(more = false) {
    const last = more && this.rows[this.rows.length - 1];
    const {page, full} = await this.read(`limit=${PAGE}` + (last ? '&before=' + last.session.id : ''));
    for (const session of page) {
      const row = this.rows.find((other) => other.session.id === session.id);
      if (row) row.session = session;
      else this.rows.push({session});
    }
    this.rows.sort((a, b) => (a.session.id < b.session.id ? 1 : -1));
    this.loaded = true;
    this.listDone = full < PAGE;
    this.drawList();
  }

  drawList() {
    if (this.listing) return;
    this.listing = true;
    queueMicrotask(() => {
      this.listing = false;
      const {context, agent} = this;
      const shown = this.rows.filter((row) => this.filter === 'all' || row.session.created_by === app.me.id);
      const open = this.current?.session?.id;
      this.list.replaceChildren(...shown.map(({session}) => {
        const owner = app.config.idp && session.created_by !== app.me.id ? person(context, session.created_by) : null;
        const stands = standing(session);
        const failure = stands.key === 'failed' && stands.turn?.failure;
        return h('a' + (session.id === open ? '.on' : ''), {href: link(to.session(context.project, agent, session)), 'aria-current': session.id === open ? 'page' : null},
          h('span.sic', stateIcon(stands.key)),
          h('span.tx', title(session),
            h('small', owner && h('span.own', avatar(owner, 'xs'), owner.name),
              h('span', time(session.created_at), ' · ', stands.key === 'waiting' ? h('span.c-wait', 'Needs approval') : standingText(stands)),
              failure && h('span.err', failure.message || failure.reason))),
          sandboxMark(session));
      }));
      if (!shown.length) {
        this.list.replaceChildren(h('p.sub.sm', {style: {padding: '8px 10px 12px'}}, this.rows.length ? 'None of these are yours' : 'No sessions yet'));
      }
      this.listMore.replaceChildren(this.listDone || !this.rows.length ? '' : h('button.btn.quiet.sm', {type: 'button', onclick: (event) => {
        event.currentTarget.disabled = true;
        this.loadList(true).catch(oops);
      }}, 'Load older'));
    });
  }

  // noteSession shows the session in view in the list as the page last got
  // it from the API.
  noteSession() {
    if (this.current?.session) this.place(this.current.session);
  }

  // ---- Opening a session ----------------------------------------------------

  leave() {
    this.unfollow?.();
    this.unfollow = null;
    clearInterval(this.cooling);
    this.cooldown = 0;
  }

  // open shows the session with the ID, or the start of a new session.
  async open(sid) {
    if (sid && this.current?.session?.id === sid) return;
    this.leave();
    const mine = ++this.opening;
    if (!sid) {
      this.current = {draft: true, views: []};
      this.drawCentre();
      this.drawDetails();
      this.drawList();
      return;
    }
    this.current = null;
    this.centre.replaceChildren(h('div.welcome', h('div', {style: {width: '420px'}}, loading(4))));
    this.side.replaceChildren();
    // What changes while the session is read is newer than what is read.
    let changed = null;
    const off = feed.sessions.on((session) => { if (session.id === sid) changed = session; });
    try {
      await feed.ready();
      const [read, page] = await Promise.all([api.get('/v1/sessions/' + sid), api.get(`/v1/sessions/${sid}/turns?limit=${PAGE}`)]);
      if (mine !== this.opening) return;
      const session = changed || read;
      if (session.agent_id !== this.agent.id) throw new NotFound(sid);
      const turns = page.turns.reverse();
      this.current = {session, access: sessionAccess(this.context, session), views: [], older: turns.length === PAGE && turns[0].seq > 1};
      this.current.views = turns.map((turn) => new TurnView(turn, this.turnEnv()));
      this.drawCentre();
      this.drawDetails();
      this.noteSession();
      this.drawList();
      const newest = this.current.views[this.current.views.length - 1];
      if (newest && !newest.turn.ended_at) this.follow(newest);
      this.toBottom();
      this.joined(this.current).catch(oops);
    } catch (error) {
      if (mine !== this.opening) return;
      const missing = error instanceof NotFound || error.status === 404;
      if (!missing) console.error(error);
      this.centre.replaceChildren(blank({
        glyph: missing ? 'search' : 'warn', title: missing ? 'No such session' : 'Could not open the session',
        text: missing ? 'It does not exist, or you do not have access to it.' : sentence(error.message),
        actions: h('a.btn.sec', {href: link(to.agent(this.context.project, this.agent))}, 'Start a new session'),
      }));
    } finally {
      off();
    }
  }

  turnEnv() {
    const {context, agent} = this;
    return {
      context, agent,
      person: (id) => person(context, id),
      mayControl: (turn) => controls(context, turn),
      answer: (turn, request, option) => this.answer(turn, request, option),
      stop: (turn) => this.stop(turn),
      again: (text) => this.fill(text),
      // Whether the turn found the sandbox asleep, and so waits for it to wake.
      wakes: (turn) => this.current?.woken === turn.id,
      drawn: (view) => this.afterDraw(view),
    };
  }

  // follow reads the events of a turn that has not ended. What the turn
  // changes of its session, such as its sandbox, comes with the stream of
  // changes.
  follow(view) {
    const current = this.current;
    this.unfollow?.();
    const stop = this.unfollow = api.events(view.turn.id, {
      onEvent: (event) => {
        if (this.current !== current) return;
        if (event.type === 'session.update') this.suggested(event.data);
        else view.apply(event);
      },
      onEnd: async () => {
        if (this.current !== current) return;
        if (this.unfollow === stop) this.unfollow = null;
        try {
          view.replace(await api.get('/v1/turns/' + view.turn.id));
        } catch (error) {
          oops(error);
        }
        if (this.current !== current) return;
        this.keyed = '';
        this.afterDraw(view);
        this.drawDetails();
      },
      onError: oops,
    });
  }

  // afterDraw brings the rest of the screen in line with the newest turn.
  afterDraw(view) {
    const current = this.current;
    if (!current || !current.views || view !== current.views[current.views.length - 1]) return;
    const key = view.status + (view.turn.ended_at ? '!' : '') + view.waiting.length;
    if (key !== this.keyed) {
      this.keyed = key;
      this.drawHeader();
      this.drawComposer();
      this.drawDetails();
    }
    if (this.stick) this.toBottom();
    this.drawJump();
  }

  toBottom() {
    if (this.scroller) this.scroller.scrollTop = this.scroller.scrollHeight;
    this.stick = true;
  }

  drawJump() {
    if (!this.jump) return;
    const live = this.current?.views?.some((view) => !view.turn.ended_at);
    this.jump.hidden = this.stick || !live;
  }

  // ---- The conversation -----------------------------------------------------

  drawCentre() {
    const current = this.current;
    this.keyed = '';
    this.header = h('div.conv-h');
    this.composer = h('div');
    this.input = h('textarea', {rows: 1, 'aria-label': 'Message', onkeydown: (event) => {
      // Enter sends, except while an input method is composing a character.
      if (event.key === 'Enter' && !event.shiftKey && !event.isComposing && event.keyCode !== 229) {
        event.preventDefault();
        this.send();
      }
    }, oninput: () => { this.fit(); this.drawBar(); }});
    this.bar = h('div.bar');
    if (current.draft) {
      this.scroller = null;
      this.jump = null;
      this.centre.replaceChildren(this.header, this.welcome(), this.composer);
    } else {
      this.thread = h('div.thread');
      this.olderBox = h('div.more-turns');
      this.thread.append(this.olderBox, ...current.views.map((view) => view.el));
      this.scroller = h('div.conv-s', {onscroll: () => {
        this.stick = this.scroller.scrollHeight - this.scroller.scrollTop - this.scroller.clientHeight < 96;
        this.drawJump();
      }}, this.thread);
      this.jump = h('button.btn.sec.sm.jump', {type: 'button', hidden: true, onclick: () => { this.toBottom(); this.drawJump(); }}, icon('dn'), 'Latest');
      this.centre.replaceChildren(this.header, this.scroller, this.jump, this.composer);
      this.drawOlder();
      if (!current.views.length) this.thread.append(h('div', {style: {paddingTop: '48px'}}, empty('chat', 'No messages yet')));
    }
    this.stick = true;
    this.drawHeader();
    this.drawComposer();
    if (!this.composer.querySelector('.gate')) this.input.focus();
  }

  welcome() {
    const {agent} = this;
    return h('div.welcome', h('div.box',
      agentTile(agent, 'xxl'),
      h('h2', label(agent)),
      agent.description && h('p', agent.description),
      h('div.facts', h('span.tag', agent.harness), h('span.tag', agent.model.name), agent.approval === 'ask' && h('span.tag.y', 'asks before it acts'))));
  }

  drawOlder() {
    const current = this.current;
    this.olderBox.replaceChildren(current.older ? h('button.btn.sec.sm', {type: 'button', onclick: (event) => {
      event.currentTarget.disabled = true;
      this.loadOlder().catch(oops);
    }}, icon('up'), 'Earlier turns') : '');
  }

  async loadOlder() {
    const current = this.current;
    const first = current.views[0].turn.seq;
    const {turns} = await api.get(`/v1/sessions/${current.session.id}/turns?limit=${PAGE}&before=${first}`);
    if (this.current !== current) return;
    const views = turns.reverse().map((turn) => new TurnView(turn, this.turnEnv()));
    const height = this.scroller.scrollHeight;
    current.views = [...views, ...current.views];
    current.older = turns.length === PAGE && views[0].turn.seq > 1;
    this.olderBox.after(...views.map((view) => view.el));
    this.drawOlder();
    this.scroller.scrollTop += this.scroller.scrollHeight - height;
    this.drawDetails();
  }

  drawHeader() {
    const current = this.current;
    if (!current) return;
    // A title that is being edited is left alone.
    if (this.header.querySelector('input.ttl')) return;
    const detailsButton = h('button.btn.sec.sm.dtl' + (this.details ? '.tog' : ''), {type: 'button', 'aria-pressed': String(this.details), onclick: () => this.toggleDetails()}, icon('panel'), 'Details');
    if (current.draft) {
      this.header.replaceChildren(h('div.tx', h('h1', h('span', 'New session'))), detailsButton);
      return;
    }
    const {session, access, views} = current;
    const newest = views[views.length - 1];
    const owner = app.config.idp && !access.own ? person(this.context, session.created_by) : null;
    const heading = access.control
      ? h('button.ttl', {type: 'button', title: 'Rename the session', onclick: () => this.rename()}, title(session, 'span'), icon('edit'))
      : title(session, 'span');
    const more = h('button.ib', {type: 'button', title: 'More', 'aria-haspopup': 'true', onclick: () => menu(more, (close) => [
      access.control && item({glyph: 'edit', text: 'Rename', onclick: () => this.rename()}, close),
      item({glyph: 'link', text: 'Copy the link', onclick: async () => toast(await toClipboard(location.href) ? 'Copied the link' : 'Could not copy the link', 'ok', 1800)}, close),
      item({glyph: 'copy', text: 'Copy the session ID', onclick: async () => toast(await toClipboard(session.id) ? 'Copied the session ID' : 'Could not copy the ID', 'ok', 1800)}, close),
      access.control && h('hr'),
      access.control && item({glyph: 'trash', text: 'End the session…', danger: true, disabled: !!session.ended_at, onclick: () => this.end()}, close),
    ], {align: 'right'})}, icon('dots', 'lg'));
    this.header.replaceChildren(
      h('div.tx', h('h1', heading),
        h('p', session.ended_at ? chip('ended', endedText(session)) : newest ? chip(newest.status, newest.status === 'waiting' ? 'Waiting for approval' : stateName(newest.status)) : chip('idle', 'No messages yet'),
          sandboxBadge(session),
          owner && h('span', {style: {display: 'inline-flex', gap: '6px', alignItems: 'center'}}, avatar(owner, 'xs'), owner.name),
          h('span', 'Started ', time(session.created_at), newest ? ` · ${plural(newest.turn.seq, 'turn')}` : '', session.ended_at ? [` · ${endedText(session).toLowerCase()} `, time(session.ended_at)] : null))),
      detailsButton, more);
  }

  rename() {
    const current = this.current;
    const session = current.session;
    const slot = this.header.querySelector('h1');
    if (!slot || slot.querySelector('input')) return;
    const input = h('input.inp.ttl', {value: session.title, placeholder: 'Untitled session', maxlength: 200, 'aria-label': 'Title of the session'});
    let done = false;
    const finish = async (save) => {
      if (done) return;
      done = true;
      const next = input.value.trim();
      if (save && next !== session.title) {
        try {
          current.session = await api.patch('/v1/sessions/' + session.id, {title: next});
          // A title that a person wrote is not replaced by a suggestion.
          current.named = true;
          this.noteSession();
        } catch (error) {
          oops(error);
        }
      }
      if (this.current !== current) return;
      // The field goes before the heading is drawn again, which leaves a
      // title that is being edited alone.
      input.remove();
      this.drawHeader();
    };
    input.addEventListener('keydown', (event) => {
      if (event.isComposing || event.keyCode === 229) return;
      if (event.key === 'Enter') { event.preventDefault(); finish(true); }
      if (event.key === 'Escape') { event.preventDefault(); finish(false); }
    });
    input.addEventListener('blur', () => finish(true));
    slot.replaceChildren(input);
    input.focus();
    input.select();
  }

  // suggested takes a title that the harness gave the session. It replaces
  // only a title that the console made from the first message.
  async suggested(data) {
    const current = this.current;
    if (typeof data.title !== 'string' || !data.title.trim() || !current.access.control || current.named) return;
    if (current.session.title && current.session.title !== current.autoTitle) return;
    try {
      current.session = await api.patch('/v1/sessions/' + current.session.id, {title: clip(data.title, 120)});
      current.autoTitle = current.session.title;
      if (this.current !== current) return;
      this.drawHeader();
      this.noteSession();
    } catch (error) {
      console.error(error);
    }
  }

  async end() {
    const current = this.current;
    const ended = await confirm({
      title: 'End this session?',
      lead: 'The files that the agent made in it are deleted. The conversation stays.',
      action: 'End session', danger: true,
      submit: async () => {
        try {
          return await api.delete('/v1/sessions/' + current.session.id);
        } catch (error) {
          if (error.code === 'session_busy') throw wrong('Stop the running turn first.');
          throw error;
        }
      },
    });
    if (!ended || this.current !== current) return;
    current.session = ended;
    toast('The session has ended', 'ok');
    this.drawHeader();
    this.drawComposer();
    this.drawDetails();
    this.noteSession();
  }

  // ---- Sending --------------------------------------------------------------

  fit() {
    this.input.style.height = 'auto';
    this.input.style.height = Math.min(this.input.scrollHeight, 192) + 'px';
  }

  fill(text) {
    if (!this.input || this.composer.querySelector('.gate')) return;
    this.input.value = text;
    this.fit();
    this.drawBar();
    this.input.focus();
  }

  // unfinished returns the turn of the session that has not ended, if any.
  unfinished() {
    const views = this.current?.views || [];
    const newest = views[views.length - 1];
    return newest && !newest.turn.ended_at ? newest : null;
  }

  drawComposer() {
    const current = this.current;
    if (!current) return;
    const gate = (glyph, content, action) => this.composer.replaceChildren(h('div.gate', icon(glyph, 'lg'), h('span', content), action));
    const fresh = h('a.btn.sec.sm', {href: link(to.agent(this.context.project, this.agent))}, icon('plus'), 'New session');
    const cannot = h('b', 'Your role cannot run sessions here.');
    if (current.draft) {
      if (!this.runs) return gate('eye', cannot);
    } else if (current.session.ended_at) {
      return gate('lock', h('b', `This session has ${endedText(current.session).toLowerCase()}.`), this.runs && fresh);
    } else if (!current.access.send) {
      const owner = person(this.context, current.session.created_by);
      return gate('eye', current.access.own ? cannot : h('b', `Only ${owner.known ? owner.name : 'its creator'} can send messages here.`));
    }
    this.input.placeholder = `Message ${label(this.agent)}…`;
    this.composer.replaceChildren(h('div.composer', this.input, this.bar));
    this.fit();
    this.drawBar();
  }

  drawBar() {
    const view = this.unfinished();
    const text = this.input.value.trim();
    const wing = () => use('#wing', {class: 'i lg f', viewBox: '0 0 36 18', 'aria-hidden': 'true'});
    const sendButton = (disabled) => h('button.send', {type: 'button', title: 'Send', disabled, onclick: () => this.send()}, this.sending ? h('span.ring') : wing());
    if (this.cooldown > 0) {
      this.bar.replaceChildren(icon('warn'), h('span.msg.bad', `The queue is full. Try again in ${this.cooldown} s.`), h('span.end', sendButton(true)));
      return;
    }
    if (view) {
      const queued = view.status === 'queued';
      const may = view.env.mayControl(view.turn);
      const doing = {queued: 'Queued', starting: 'Starting…', waiting: 'Waiting for approval', stopping: 'Stopping…'}[view.status] || 'Working…';
      this.bar.replaceChildren(view.status === 'waiting' ? icon('shield') : h('span.ring'),
        h('span.msg', doing),
        h('span.end',
          may && view.status !== 'stopping' && h('button.btn.sec.sm', {type: 'button', onclick: () => this.stop(view.turn)}, icon('stop'), queued ? 'Cancel turn' : 'Stop turn'),
          sendButton(true)));
      return;
    }
    this.bar.replaceChildren(h('span.msg', 'Enter to send · Shift + Enter for a new line'), h('span.end', sendButton(!text || this.sending)));
  }

  cool(seconds) {
    this.cooldown = seconds;
    clearInterval(this.cooling);
    this.cooling = setInterval(() => {
      this.cooldown--;
      if (this.cooldown <= 0) clearInterval(this.cooling);
      this.drawBar();
    }, 1000);
    this.drawBar();
  }

  async send() {
    const text = this.input.value.trim();
    if (!text || this.sending || this.cooldown > 0 || this.unfinished()) return;
    this.sending = true;
    this.drawBar();
    let current = this.current;
    try {
      if (current.draft) {
        // The session is created with its first message, and named after it.
        const named = suggest(text);
        const session = await api.post(`/v1/agents/${this.agent.id}/sessions`, {title: named});
        current = this.current = {session, access: sessionAccess(this.context, session), views: [], older: false, autoTitle: named};
        this.place(session);
        this.drawCentre();
        // The message is kept in the box until it has been accepted.
        this.input.value = text;
        this.drawDetails();
        this.drawList();
        go(to.session(this.context.project, this.agent, session), {replace: true});
      }
      const asleep = current.session.sandbox === 'asleep';
      const turn = await api.post(`/v1/sessions/${current.session.id}/messages`, {text});
      if (this.current !== current) return;
      if (asleep) current.woken = turn.id;
      this.input.value = '';
      // The stream of changes may have shown the turn already.
      if (current.views.some((other) => other.turn.id === turn.id)) return;
      const view = new TurnView({...turn, items: []}, this.turnEnv());
      if (!current.views.length) this.thread.replaceChildren(this.olderBox);
      current.views.push(view);
      this.thread.append(view.el);
      this.toBottom();
      this.follow(view);
      this.afterDraw(view);
      this.drawDetails();
    } catch (error) {
      if (error.code === 'queue_full') {
        this.cool(error.retryAfter || 5);
      } else if (error.code === 'session_busy' || error.code === 'session_ended') {
        toast(sentence(error.message), 'bad');
        const sid = current.session.id;
        this.current = null;
        await this.open(sid);
      } else {
        oops(error);
      }
    } finally {
      this.sending = false;
      if (this.current && this.input) { this.fit(); this.drawBar(); }
    }
  }

  async stop(turn) {
    const current = this.current;
    const view = current.views.find((other) => other.turn.id === turn.id);
    try {
      const record = await api.post(`/v1/turns/${turn.id}/cancel`);
      if (this.current === current && view && !view.turn.ended_at) view.replace({...record, items: view.items});
    } catch (error) {
      if (error.code !== 'turn_ended') oops(error);
    }
  }

  // answer sends the answer to an approval request, and reports whether the
  // request now has one.
  async answer(turn, request, option) {
    try {
      await api.post(`/v1/turns/${turn.id}/approvals/${request.id}`, {option_id: option.id});
      return true;
    } catch (error) {
      if (error.code === 'approval_answered') {
        toast('Someone has already answered this request.');
        return true;
      }
      oops(error);
      return false;
    }
  }

  // ---- The details ----------------------------------------------------------

  drawDetails() {
    const {context, agent, provider} = this;
    const current = this.current;
    if (!current) return;
    const top = h('div.r-top', h('h2.h3', current.draft ? 'About this agent' : 'Session details'),
      h('button.ib.sm', {type: 'button', title: 'Close', onclick: () => this.toggleDetails()}, icon('chev')));
    const about = h('div.r-s',
      h('h4', agentTile(agent, 'xs'), h('a', {href: link(to.agents(context.project))}, label(agent))),
      h('dl.kv',
        h('dt', 'Slug'), h('dd', h('span.tag', agent.slug)),
        h('dt', 'Harness'), h('dd', h('span.tag', agent.harness)),
        h('dt', 'Model'), h('dd', h('span.tag', agent.model.name)),
        h('dt', 'Provider'), h('dd', provider ? provider.name : 'Unknown'),
        h('dt', 'Approval'), h('dd', agent.approval === 'ask' ? h('span.tag.y', 'ask') : h('span.sub', 'None'))),
      agent.description && h('p.about', agent.description));
    if (current.draft) {
      this.side.replaceChildren(top, h('div.pane-s', about));
      return;
    }
    const {session, views} = current;
    const owner = person(context, session.created_by);
    const newest = views[views.length - 1];
    const counts = {};
    for (const view of views) counts[view.turn.state] = (counts[view.turn.state] || 0) + 1;
    const shown = Object.entries(counts).map(([state, count]) => `${count} ${state}`).join(', ');
    const used = views.filter((view) => view.turn.usage);
    const sum = (field) => used.reduce((total, view) => total + (view.turn.usage[field] || 0), 0);
    const partial = newest && views.length < newest.turn.seq;
    this.side.replaceChildren(top, h('div.pane-s',
      h('div.r-s',
        h('div.idb', {title: 'Session ID'}, h('span', session.id), copyButton(session.id, 'session ID')),
        h('dl.kv', {style: {marginTop: '12px'}},
          app.config.idp && [h('dt', 'Owner'), h('dd', avatar(owner, 'xs'), h('span', {title: owner.title || ''}, owner.name))],
          h('dt', 'Started'), h('dd', time(session.created_at)),
          session.ended_at && [h('dt', endedText(session)), h('dd', time(session.ended_at))],
          h('dt', 'Turns'), h('dd', newest ? newest.turn.seq : 0, shown && h('small', partial ? `of those shown: ${shown}` : shown)),
          h('dt', 'Sandbox'), h('dd', sandboxBadge(session, true)))),
      h('div.r-s',
        h('h4', 'Tokens', partial && h('small.sub', {style: {fontWeight: 500}}, 'of the turns shown')),
        h('div.tok', h('div', h('b', num(sum('input_tokens'))), h('small', 'in')), h('div', h('b', num(sum('output_tokens'))), h('small', 'out')))),
      about));
  }
}

export async function workspaceView(root, params, env) {
  const context = await project(params.project);
  const agent = context.agents.find((other) => other.slug === params.agent);
  if (!agent) throw new NotFound(params.agent);
  const workspace = new Workspace(root, context, agent, env);
  await workspace.open(params.sid || null);
  return {
    context,
    cleanup: () => workspace.close(),
    // update shows another session of the same agent without drawing the
    // workspace again. An agent that was changed is drawn again.
    update(next) {
      if (next.project !== params.project || next.agent !== params.agent || !context.agents.includes(agent)) return false;
      workspace.open(next.sid || null).catch(oops);
      return true;
    },
  };
}
