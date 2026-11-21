// How a session stands, for the lists of sessions.

import {h, icon, clip} from './util.js';
import {status, stateName} from './ui.js';

// standing returns how a session stands: {key, turn}. turn is its newest
// turn, which the session carries. key is the state of that turn, "ended" for
// a session that has ended, or "none" for one without turns.
export function standing(session, turn = session.last_turn) {
  if (turn && !turn.ended_at) return {key: status(turn), turn};
  if (session.ended_at) return {key: 'ended', turn, ended: endedText(session)};
  return {key: turn ? status(turn) : 'none', turn};
}

// endedText says how a session ended: it expired, or it was ended.
export function endedText(session) {
  return session.end_reason === 'expired' ? 'Expired' : 'Ended';
}

export function standingText(stands) {
  if (stands.key === 'ended') return stands.ended;
  if (stands.key === 'none') return 'No messages yet';
  return stateName(stands.key);
}

// title returns the title of a session as a node.
export function title(session, tag = 'b') {
  if (session.title) return h(tag, session.title);
  return h(tag + '.untitled', 'Untitled session');
}

// sandboxBadge says whether the sandbox of a session is awake, asleep or
// lost. If always is set, it also says that there is none: not yet, or no
// longer.
export function sandboxBadge(session, always = false) {
  if (session.ended_at) return always ? h('span.sb.gone', 'Deleted') : null;
  if (session.sandbox === 'awake') return h('span.sb.awake', {title: 'Sandbox awake'}, 'Awake');
  if (session.sandbox === 'asleep') return h('span.sb.asleep', {title: 'Sandbox asleep'}, icon('moon'), 'Asleep');
  if (session.sandbox === 'lost') return h('span.sb.lost', {title: 'Sandbox lost'}, icon('warn'), 'Lost');
  return always ? h('span.sb.gone', 'Not started') : null;
}

// What sandboxMark says of each state, and the icon that it shows.
const MARKS = {awake: ['Sandbox awake'], asleep: ['Sandbox asleep', 'moon'], lost: ['Sandbox lost', 'warn']};

// sandboxMark is the badge without its word, for a list.
export function sandboxMark(session) {
  const mark = !session.ended_at && MARKS[session.sandbox];
  if (!mark) return null;
  const [label, glyph] = mark;
  return h('span.sbm.' + session.sandbox, {title: label, role: 'img', 'aria-label': label}, glyph ? icon(glyph) : null);
}

// suggest makes a title from the first message of a session.
export function suggest(text) {
  return clip(text.split('\n').find((line) => line.trim()) || text, 72);
}
