// The requests that the console has sent, for the panel of the mock that
// lists them. Not part of the product.

import {emitter} from './util.js';

const KEPT = 500;

export const entries = [];
export const logged = emitter();

let pending = false;
// touch tells the panel that an entry changed. Bursts of events are told once.
export function touch() {
  if (pending) return;
  pending = true;
  setTimeout(() => { pending = false; logged.emit(); }, 60);
}

export function record(entry) {
  entries.push(entry);
  if (entries.length > KEPT) entries.splice(0, entries.length - KEPT);
  touch();
  return entry;
}

export function clear() {
  entries.length = 0;
  touch();
}
