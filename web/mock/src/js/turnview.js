// One turn of a conversation: the message that was sent, and what the agent
// said and did. A turn that has ended is drawn from the items of its record.
// A turn that has not is drawn from its events, assembled into the same
// items, and drawn again as they arrive.

import {h, icon, time, clock, duration, num} from './util.js';
import {markdown, codeBlock} from './markdown.js';
import {add} from './items.js';
import {avatar, agentTile, label, chip, status, stateName} from './ui.js';

const KINDS = {read: 'doc', edit: 'edit', delete: 'trash', move: 'arrow', search: 'search', execute: 'term', think: 'think', fetch: 'globe'};

// What each failure reason of the API means for the person who reads it.
const FAILURES = {
  harness_error: 'The agent ran into an error',
  resume_failed: 'The agent could not pick up the conversation',
  startup_timed_out: 'The agent did not start in time',
  timed_out: 'The turn took too long and was stopped',
  sandbox_lost: 'The sandbox was lost, with the files in it',
  session_expired: 'The session was unused for too long and has ended',
  runner_error: 'Something went wrong',
};

const STOPS = {
  max_tokens: 'Stopped at the output limit of the model.',
  max_turn_requests: 'Stopped at the limit of model requests.',
  refusal: 'The model refused to continue.',
  cancelled: 'Ended because an action was refused.',
};

const json = (value) => JSON.stringify(value, null, 2);

// findDiff looks for a unified diff in the input or output of a tool call.
function findDiff(value, depth = 0) {
  if (typeof value === 'string') return /^(@@ |--- |\+\+\+ )/m.test(value) && /^[+-]/m.test(value) ? value : null;
  if (!value || typeof value !== 'object' || depth > 3) return null;
  for (const inner of Object.values(value)) {
    const found = findDiff(inner, depth + 1);
    if (found) return found;
  }
  return null;
}

// outputText returns the text of a tool's output, if it has one. Harnesses
// shape their output differently, and these are the fields they use.
function outputText(output) {
  if (typeof output === 'string') return output;
  if (!output || typeof output !== 'object') return '';
  for (const key of ['output', 'stdout', 'formatted_output', 'aggregated_output', 'content', 'error', 'stderr']) {
    if (typeof output[key] === 'string' && output[key]) return output[key];
  }
  return '';
}

function exitCode(output) {
  const code = output?.exit_code ?? output?.metadata?.exit;
  return typeof code === 'number' ? code : null;
}

function command(input, kind = 'execute', inside = false) {
  return h('div.cmd' + (inside ? '.in' : ''), h('i', '$'), h('span', input.command, input.cwd && inside ? h('small', 'in ' + input.cwd) : null), !inside && h('span.tag', kind));
}

const changed = (input) => {
  const before = input.oldString ?? input.old_string ?? input.old_str;
  const after = input.newString ?? input.new_string ?? input.new_str;
  if (typeof before !== 'string' || typeof after !== 'string') return null;
  const lines = (text, sign) => (text ? text.split('\n').map((line) => sign + ' ' + line) : []);
  return ['@@', ...lines(before, '-'), ...lines(after, '+')].join('\n');
};

const pathOf = (input) => input?.filePath || input?.file_path || input?.path || '';

export class TurnView {
  // env is {context, agent, person(id), mayControl(turn), answer(turn, request, option), stop(turn), again(text)}.
  constructor(turn, env) {
    this.env = env;
    this.opened = new Set();
    this.answering = new Map();
    this.position = 0;
    this.el = h('div.turn', {id: 'turn-' + turn.seq});
    this.replace(turn);
  }

  // replace takes a record of the turn from the API.
  replace(record) {
    const {items = [], approvals = [], ...turn} = record;
    this.turn = turn;
    if (turn.ended_at || !this.items || !this.live) {
      this.items = items.map((item) => ({...item}));
      // The requests that wait are listed with a turn that has not ended,
      // so that they show before its events have been read.
      for (const request of approvals) add(this.items, 'approval.requested', request);
    }
    this.live = !turn.ended_at;
    this.draw();
  }

  // apply takes the next event of the turn.
  apply(event) {
    const {type, data} = event;
    const turn = this.turn;
    // The stream begins with the first event of the turn, so the items are
    // assembled from nothing: what the record listed comes again, in place.
    if (!this.fed) {
      this.fed = true;
      this.items = [];
    }
    switch (type) {
      case 'turn.queued': this.position = data.position; break;
      case 'turn.started':
        turn.state = 'running';
        turn.started_at ||= new Date().toISOString();
        break;
      case 'turn.completed': Object.assign(turn, {state: 'completed', stop_reason: data.stop_reason, usage: data.usage, ended_at: turn.ended_at || new Date().toISOString()}); break;
      case 'turn.failed': Object.assign(turn, {state: 'failed', failure: data, ended_at: turn.ended_at || new Date().toISOString()}); break;
      case 'turn.cancelled': Object.assign(turn, {state: 'cancelled', ended_at: turn.ended_at || new Date().toISOString()}); break;
      case 'turn.expired': Object.assign(turn, {state: 'expired', ended_at: turn.ended_at || new Date().toISOString()}); break;
      default:
        if (turn.state === 'queued') turn.state = 'running';
        if (!add(this.items, type, data)) return;
        if (type === 'approval.resolved') this.answering.delete(data.id);
    }
    this.schedule();
  }

  get waiting() {
    return this.items.filter((item) => item.type === 'approval' && !item.outcome);
  }

  // A turn that had to wait gets a turn.queued event with its place in the
  // queue. One without a place was taken at once, and is only waiting for its
  // sandbox: the record still says "queued" until its harness has the message.
  get status() {
    const key = status(this.turn, this.waiting.length > 0);
    return key === 'queued' && !this.position ? 'starting' : key;
  }

  schedule() {
    if (this.pending) return;
    this.pending = requestAnimationFrame(() => { this.pending = 0; this.draw(); });
  }

  draw() {
    if (this.pending) { cancelAnimationFrame(this.pending); this.pending = 0; }
    const {turn, env} = this;
    const key = this.status;
    const sender = env.person(turn.created_by);
    const agent = env.agent;
    const words = {queued: 'is in the queue', starting: 'is starting', running: 'is working', waiting: 'is waiting for an answer', stopping: 'is stopping'}[key];
    this.el.replaceChildren(
      h('div.turn-h', h('b', 'Turn ', turn.seq), chip(key, key === 'queued' && this.position ? `Queued · ${this.position}` : stateName(key)), this.headline(key)),
      h('div.msg-u', h('div.bub-u', turn.text), avatar(sender)),
      h('div.ag', h('div.ag-av', agentTile(agent, 'md')),
        h('div.ag-c', h('div.ag-n', label(agent), words && h('small', words)), this.blocks(key), this.footer(key))));
    this.env.drawn?.(this);
  }

  headline(key) {
    const turn = this.turn;
    if (turn.ended_at && turn.started_at) return [time(turn.created_at), ' · ', duration(Date.parse(turn.ended_at) - Date.parse(turn.started_at))];
    if (key === 'running' || key === 'waiting' || key === 'stopping') return ['started ', h('time', {title: turn.started_at}, clock(turn.started_at))];
    return time(turn.created_at);
  }

  // remember keeps a <details> open or closed across redraws.
  remember(node, key, open = false) {
    node.open = this.opened.has(key) ? true : this.opened.has('!' + key) ? false : open;
    node.addEventListener('toggle', () => {
      this.opened.delete(key);
      this.opened.delete('!' + key);
      this.opened.add(node.open ? key : '!' + key);
    });
    return node;
  }

  blocks(key) {
    const items = this.items;
    const asked = new Map(items.filter((item) => item.type === 'approval' && item.tool_call?.id).map((item) => [item.tool_call.id, item]));
    const tools = new Set(items.filter((item) => item.type === 'tool_call').map((item) => item.id));
    const out = [];
    let steps = null;
    items.forEach((item, index) => {
      if (item.type === 'tool_call') {
        // A tool call that waits for approval is shown by its request.
        if (asked.get(item.id) && !asked.get(item.id).outcome) return;
        if (!steps) out.push(steps = h('div.steps'));
        steps.append(this.step(item, asked.get(item.id)));
        return;
      }
      if (item.type === 'approval') {
        if (!item.outcome) { steps = null; out.push(this.request(item)); }
        else if (!tools.has(item.tool_call?.id)) { steps = null; out.push(this.answered(item, true)); }
        return;
      }
      steps = null;
      if (item.type === 'message') {
        const bubble = h('div.bub-a', markdown(item.text));
        if (index === items.length - 1 && key === 'running') (bubble.querySelector('.md > :last-child') || bubble).append(h('span.caret'));
        out.push(bubble);
      } else if (item.type === 'thought') {
        out.push(this.remember(h('details.th', h('summary', icon('think'), 'Thought', icon('chev', 'chev')), h('p', item.text)), 'th' + index));
      } else if (item.type === 'plan') {
        out.push(this.plan(item));
      }
    });
    return out;
  }

  plan(item) {
    const entries = item.entries || [];
    const done = entries.filter((entry) => entry.status === 'completed').length;
    return h('div.plan',
      h('div.plan-h', icon('list'), 'Plan',
        h('span.pips', entries.map((entry) => h('i' + (entry.status === 'completed' ? '.done' : entry.status === 'in_progress' ? '.now' : '')))),
        h('small' + (done === entries.length ? '.all' : ''), `${done} of ${entries.length} done`)),
      h('ol', entries.map((entry) => h('li.' + entry.status,
        entry.status === 'completed' ? h('span.c-ok', icon('ok', 'lg')) : entry.status === 'in_progress' && !this.turn.ended_at ? h('span.ring') : h('span.todo'),
        h('span', entry.content), entry.priority === 'high' && h('span.pri', 'high priority')))));
  }

  step(item, approval) {
    const input = item.input && typeof item.input === 'object' ? item.input : {};
    const kind = item.kind || 'other';
    const over = !!this.turn.ended_at;
    const mark = item.status === 'completed' ? h('span.c-ok', icon('ok', 'lg')) : item.status === 'failed' ? h('span.c-fail', icon('fail', 'lg'))
      : over ? h('span.c-grey', {title: 'Did not finish'}, icon('cancel', 'lg'))
        : item.status === 'in_progress' ? h('span.ring') : h('span.c-grey', {title: 'Pending'}, icon('clock', 'lg'));
    const diff = findDiff(item.output) || findDiff(item.input) || changed(input);
    const output = outputText(item.output);
    const code = exitCode(item.output);
    const body = [];
    if (approval) body.push(this.answered(approval));
    if (typeof input.command === 'string') body.push(command(input, kind, true));
    else if (pathOf(input)) body.push(h('p.sub.xs.mono', pathOf(input)));
    if (diff) body.push(codeBlock(diff, 'diff'));
    else if (output) body.push(h('pre.code', output));
    if (code != null && (code !== 0 || !output)) body.push(h('p.sub.xs', 'Exited with code ', h('b', code), '.'));
    if (item.status === 'failed' && !output && typeof item.output?.error !== 'string') body.push(h('p.sub.xs', 'The tool call failed.'));
    const raw = Object.fromEntries(['input', 'output', 'meta'].filter((field) => item[field] !== undefined).map((field) => [field, item[field]]));
    if (Object.keys(raw).length) {
      body.push(this.remember(h('details.raw', h('summary', icon('chev', 'chev'), 'Raw'), h('pre.code', json(raw))), 'raw' + item.id));
    }
    const title = item.title || kind;
    return this.remember(h('details.step',
      h('summary', h('span.kd' + (approval ? '.y' : ''), icon(approval ? 'shield' : KINDS[kind] || 'cube')),
        h('span.step-t' + (kind === 'execute' && title === input.command ? '.cmdline' : ''), title),
        h('span.tag', kind), mark, icon('chev', 'chev')),
      h('div.step-b', body)), 'step' + item.id);
  }

  // answered is the line that says how an approval request ended.
  answered(item, alone = false) {
    const option = (item.options || []).find((other) => other.id === item.option_id);
    const by = item.answered_by ? this.env.person(item.answered_by) : null;
    if (item.outcome === 'selected' && option) {
      const allowed = option.kind.startsWith('allow');
      return h('p.answer' + (allowed ? '' : '.no') + (alone ? '.alone' : ''), icon('shield'),
        h('span', alone && item.tool_call?.title ? [h('b', item.tool_call.title), ': '] : null, h('b', option.name), by ? [' by ', by.name] : null));
    }
    return h('p.answer.no' + (alone ? '.alone' : ''), icon('shield'), h('span', 'Not answered'));
  }

  // request is the card of an approval request that waits for an answer.
  request(item) {
    const {env, turn} = this;
    const call = item.tool_call || {};
    const input = call.input && typeof call.input === 'object' ? call.input : {};
    const may = env.mayControl(turn);
    const sending = this.answering.get(item.id);
    const diff = findDiff(input) || changed(input);
    const what = typeof input.command === 'string' ? 'May I run this command?' : call.kind === 'edit' ? 'May I change this file?' : 'May I do this?';
    const shown = typeof input.command === 'string' ? command(input, call.kind || 'execute')
      : [h('div.cmd', h('i', '›'), h('span', pathOf(input) || call.title || call.kind), h('span.tag', call.kind || 'tool')),
        diff ? codeBlock(diff, 'diff') : Object.keys(input).length && !pathOf(input) ? h('pre.code', json(input)) : null];
    const styles = {allow_once: 'pri', allow_always: 'sec', reject_once: 'dq', reject_always: 'dq'};
    const options = item.options || [];
    const tall = options.some((option) => option.name.length > 26);
    return h('div.appr', {role: 'group', 'aria-label': 'Request for approval'},
      h('div.appr-h', h('span.appr-i', icon('shield', 'lg')), h('div', h('h3', what))),
      shown,
      !may ? h('p.appr-w', icon('info'), 'Waiting for ', env.person(turn.created_by).name, ' to answer')
        : sending ? h('p.appr-w', h('span.ring'), h('b', sending))
          : h('div.appr-a' + (tall ? '.tall' : ''),
            options.map((option) => h('button.btn.' + (styles[option.kind] || 'sec'), {type: 'button', onclick: () => this.choose(item, option)}, option.name)),
            h('span.sp'),
            h('button.btn.grey', {type: 'button', onclick: () => env.stop(turn)}, icon('stop'), 'Stop turn')));
  }

  async choose(item, option) {
    this.answering.set(item.id, option.name);
    this.draw();
    const sent = await this.env.answer(this.turn, item, option);
    // The card stays until the harness has the answer and says so.
    if (!sent) { this.answering.delete(item.id); this.draw(); }
  }

  // footer says how the turn stands, below what the agent wrote.
  footer(key) {
    const {turn, env} = this;
    const may = env.mayControl(turn);
    if (key === 'queued') {
      return h('p.state', h('span.c-dusk', icon('clock', 'lg')),
        h('span', 'In the queue', this.position ? [', ', h('b', this.position === 1 ? 'next to run' : `${this.position - 1} ahead`)] : null),
        may && h('button.btn.grey.sm', {type: 'button', onclick: () => env.stop(turn)}, 'Cancel'));
    }
    if (key === 'starting') return h('p.state', h('span.ring'), h('span', env.wakes?.(turn) ? 'Waking up…' : 'Starting…'));
    if (key === 'stopping') return h('p.state', h('span.ring'), h('span', 'Stopping…'));
    if (key === 'running') return this.items.length ? null : h('p.state', h('span.ring'), h('span', 'Working…'));
    if (key === 'waiting') return null;
    if (key === 'completed') {
      const usage = turn.usage;
      const more = usage && [['thought_tokens', 'thought'], ['cached_read_tokens', 'read from cache'], ['cached_write_tokens', 'written to cache']]
        .filter(([field]) => usage[field]).map(([field, text]) => `${num(usage[field])} ${text}`).join(', ');
      return h('p.done-l',
        turn.stop_reason && STOPS[turn.stop_reason] ? [h('span.c-wait', STOPS[turn.stop_reason]), usage && h('span.sep', '·')] : null,
        usage && h('span', {title: more || null}, `${num(usage.input_tokens)} tokens in · ${num(usage.output_tokens)} out`));
    }
    const again = env.again && h('div.acts', h('button.btn.sec.sm', {type: 'button', onclick: () => env.again(turn.text)}, icon('refresh'), 'Send again'));
    if (key === 'failed') {
      return h('div.fail', icon('warn', 'lg'), h('b', FAILURES[turn.failure?.reason] || 'The turn failed'),
        turn.failure?.message && h('pre.code.wrapped', turn.failure.message), again);
    }
    if (key === 'expired') return h('div.fail.soft', icon('hour', 'lg'), h('b', 'This turn waited too long in the queue'), again);
    return h('div.fail.soft', icon('cancel', 'lg'), h('b', turn.started_at ? 'Stopped' : 'Cancelled before it started'));
  }
}
