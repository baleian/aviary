// What changes in the project in view. The console follows one stream of
// changes for the project (see changes() in api.js), and the views take from
// it what they show, so that none of them asks the API again and again.

import {api} from './api.js';
import {emitter} from './util.js';

let watched = null;
let stop = () => {};
let open = false;
let opens = 0;
let waiting = [];
// The sessions as the feed last knew them, by ID.
const known = new Map();

function settle() {
  for (const resolve of waiting.splice(0)) resolve();
}

export const feed = {
  // Emitted with (session, before) when a session of the project has changed:
  // the session as it is now, and as the feed knew it before, if it did.
  sessions: emitter(),
  // Emitted when the stream has opened again after it broke. It may have
  // missed changes, so the views read again what they show.
  reopened: emitter(),

  // watch follows the project with the ID, and no other. Without an ID it
  // follows none.
  watch(projectID = null) {
    if (projectID === watched) return;
    stop();
    stop = () => {};
    known.clear();
    watched = projectID;
    open = false;
    opens = 0;
    if (!watched) {
      settle();
      return;
    }
    stop = api.changes(projectID, {
      onSession: (session) => {
        if (watched !== projectID) return;
        const before = known.get(session.id) || null;
        known.set(session.id, session);
        feed.sessions.emit(session, before);
      },
      onOpen: () => {
        if (watched !== projectID) return;
        open = true;
        settle();
        if (opens++) feed.reopened.emit();
      },
      // Without the stream the console shows what it read, and nothing moves.
      onError: () => { if (watched === projectID) settle(); },
    });
  },

  // ready resolves once the stream of the project in view is open, or cannot
  // be opened. What is read after that misses no change.
  ready() {
    if (!watched || open) return Promise.resolve();
    return new Promise((resolve) => waiting.push(resolve));
  },

  // note tells the feed how a session was when a view read it, so that the
  // feed can tell what a later change changed.
  note(session) {
    if (!known.has(session.id)) known.set(session.id, session);
  },
};
