// Small helpers: building DOM, formatting, IDs.

const SVG = 'http://www.w3.org/2000/svg';
const PROPERTIES = new Set(['hidden', 'disabled', 'checked', 'open', 'value', 'selected', 'readOnly', 'required', 'indeterminate']);

// h('div.card.agent', {onclick: f}, child, ...) builds an element. The tag may
// carry classes and an ID. Children are nodes, strings or arrays of them;
// null and false are skipped.
export function h(tag, ...rest) {
  const [name, ...classes] = tag.split('.');
  const [element, id] = name.split('#');
  const node = document.createElement(element || 'div');
  if (id) node.id = id;
  if (classes.length) node.className = classes.join(' ');
  let children = rest;
  const first = rest[0];
  if (first && typeof first === 'object' && !(first instanceof Node) && !Array.isArray(first)) {
    children = rest.slice(1);
    for (const [key, value] of Object.entries(first)) {
      if (value == null || value === false) continue;
      if (key === 'class') node.className = [node.className, value].filter(Boolean).join(' ');
      else if (key === 'style' && typeof value === 'object') {
        for (const [name, set] of Object.entries(value)) {
          if (name.startsWith('--')) node.style.setProperty(name, set);
          else node.style[name] = set;
        }
      }
      else if (key === 'dataset') Object.assign(node.dataset, value);
      else if (key.startsWith('on') && typeof value === 'function') node.addEventListener(key.slice(2), value);
      else if (PROPERTIES.has(key)) node[key] = value;
      else node.setAttribute(key, value === true ? '' : value);
    }
  }
  append(node, children);
  return node;
}

export function append(node, children) {
  for (const child of children) {
    if (child == null || child === false || child === '') continue;
    if (Array.isArray(child)) append(node, child);
    else node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return node;
}

// use('#logo', {width: 30, height: 30}) returns an <svg> that shows a symbol
// of the sprite.
export function use(symbol, attrs = {}) {
  const svg = document.createElementNS(SVG, 'svg');
  for (const [key, value] of Object.entries(attrs)) {
    if (value != null && value !== false) svg.setAttribute(key, value);
  }
  const ref = document.createElementNS(SVG, 'use');
  ref.setAttribute('href', symbol);
  svg.append(ref);
  return svg;
}

export function icon(name, more = '') {
  return use('#i-' + name, {class: ('i ' + more).trim(), 'aria-hidden': 'true'});
}

export function code(text) {
  return h('code', text);
}

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
const two = (n) => String(n).padStart(2, '0');

export function clock(value) {
  const date = new Date(value);
  return two(date.getHours()) + ':' + two(date.getMinutes());
}

// when returns a short form of a time: "Today 14:02", "Yesterday 09:15",
// "Mon 16:20" within a week, then "Nov 12".
export function when(value) {
  const date = new Date(value);
  const now = new Date();
  const midnight = (d) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const days = Math.round((midnight(now) - midnight(date)) / 86400000);
  if (days === 0) return 'Today ' + clock(date);
  if (days === 1) return 'Yesterday ' + clock(date);
  if (days > 1 && days < 7) return DAYS[date.getDay()] + ' ' + clock(date);
  const day = MONTHS[date.getMonth()] + ' ' + date.getDate();
  return date.getFullYear() === now.getFullYear() ? day : day + ', ' + date.getFullYear();
}

export function fullTime(value) {
  const date = new Date(value);
  return `${MONTHS[date.getMonth()]} ${date.getDate()}, ${date.getFullYear()}, ${clock(date)}:${two(date.getSeconds())}`;
}

// time returns a <time> that shows the short form and, on hover, the full one.
export function time(value, format = when) {
  return h('time', {datetime: new Date(value).toISOString(), title: fullTime(value)}, format(value));
}

export function duration(ms) {
  const seconds = Math.max(0, Math.round(ms / 1000));
  if (seconds < 60) return seconds + 's';
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${two(seconds % 60)}s`;
  return `${Math.floor(minutes / 60)}h ${two(minutes % 60)}m`;
}

export function num(value) {
  return Number(value).toLocaleString('en-US');
}

export function plural(count, one, many = one + 's') {
  return `${num(count)} ${count === 1 ? one : many}`;
}

export function initials(name) {
  const words = String(name || '?').replace(/@.*/, '').split(/[\s._-]+/).filter(Boolean);
  const letters = words.length > 1 ? words[0][0] + words[words.length - 1][0] : (words[0] || '?').slice(0, 2);
  return letters.toUpperCase();
}

// pick returns a stable number below count for a string, so that the same
// agent or person always gets the same colour.
export function pick(text, count) {
  let sum = 0;
  for (const ch of String(text)) sum = (sum * 31 + ch.codePointAt(0)) >>> 0;
  return sum % count;
}

export function shortID(id) {
  return String(id).slice(0, 8);
}

const SLUG = /^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$/;

export function isSlug(text) {
  return SLUG.test(text);
}

// slugify suggests a slug for a name.
export function slugify(name) {
  return String(name).toLowerCase().normalize('NFKD').replace(/[^a-z0-9]+/g, '-').replace(/^[^a-z]+/, '').replace(/-+$/, '').slice(0, 63).replace(/-+$/, '');
}

export function clip(text, longest) {
  const flat = String(text).replace(/\s+/g, ' ').trim();
  return flat.length > longest ? flat.slice(0, longest - 1).trimEnd() + '…' : flat;
}

// uuid7 returns a UUID of version 7 for a time. IDs made later sort later.
let lastStamp = 0;
let sequence = 0;
export function uuid7(ms = Date.now(), random = Math.random) {
  if (ms === lastStamp) sequence++;
  else { lastStamp = ms; sequence = 0; }
  const hex = (n, width) => Math.floor(n).toString(16).padStart(width, '0').slice(-width);
  const stamp = hex(ms, 12);
  const a = hex(sequence + random() * 0x100, 3);
  const b = hex(0x8000 + random() * 0x3fff, 4);
  const c = hex(random() * 0xffffff, 6) + hex(random() * 0xffffff, 6);
  return `${stamp.slice(0, 8)}-${stamp.slice(8)}-7${a}-${b}-${c}`;
}

// seeded returns a random number generator that repeats for a seed, so that
// the example data has the same IDs after every reload.
export function seeded(seed) {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export function copy(value) {
  return JSON.parse(JSON.stringify(value));
}

// prefs keeps small settings of the browser, such as the theme. Storage can
// be unavailable, and then the settings last until the page is closed.
const memory = {};
export const prefs = {
  get(key, absent = null) {
    try {
      const value = localStorage.getItem('aviary.' + key);
      return value == null ? (key in memory ? memory[key] : absent) : value;
    } catch (error) {
      return key in memory ? memory[key] : absent;
    }
  },
  set(key, value) {
    memory[key] = value;
    try {
      if (value == null) localStorage.removeItem('aviary.' + key);
      else localStorage.setItem('aviary.' + key, value);
    } catch (error) { /* kept in memory */ }
  },
};

export async function toClipboard(text) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch (error) {
    return false;
  }
}

// emitter returns a list of listeners: on(f) adds one and returns a function
// that removes it, and emit(...) calls them all.
export function emitter() {
  const listeners = new Set();
  return {
    on(listener) { listeners.add(listener); return () => listeners.delete(listener); },
    emit(...args) { for (const listener of [...listeners]) listener(...args); },
  };
}
