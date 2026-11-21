// The client of the Supervisor's API. The views call only this module.
//
// In the mock, send(), events() and changes() hand the request to backend.js.
// In the product they are fetch() calls against /v1 with the caller's token:
// the streams with fetch too, because EventSource cannot send the
// Authorization header. Nothing else in this file, and nothing in the views,
// changes.

import {request, open, watch, flagOf, settings} from './backend.js';
import {record, touch} from './log.js';
import {sleep, prefs, emitter} from './util.js';

export class ApiError extends Error {
  constructor(status, body, headers = {}) {
    super(body?.message || `the request failed with status ${status}`);
    this.status = status;
    this.code = body?.code || '';
    this.retryAfter = Number(headers['Retry-After']) || 0;
  }
}

// The token of the signed-in caller. The mock keeps it across reloads, as a
// product would keep the session of its identity provider.
let token = prefs.get('mock.token', '');

export function signedInAs() {
  return token;
}

export function signIn(value) {
  token = value;
  prefs.set('mock.token', value || null);
}

// Emitted when a request is refused with 401: the token is missing, expired
// or not from this installation's identity provider.
export const unauthenticated = emitter();

// How long the mock takes to answer, in ms: between the two.
export const network = {delay: [60, 170]};

async function send(method, path, body, note) {
  const entry = record({at: Date.now(), method, path, status: 0, ms: 0, flag: flagOf(method, path), note: note || ''});
  const started = performance.now();
  const [least, most] = network.delay;
  await sleep(most > 0 ? least + Math.random() * (most - least) : 0);
  const response = request(method, path, {token, body: body === undefined ? null : JSON.parse(JSON.stringify(body))});
  entry.status = response.status;
  entry.ms = Math.round(performance.now() - started);
  touch();
  if (response.status >= 400) {
    const error = new ApiError(response.status, response.body, response.headers);
    if (response.status === 401) unauthenticated.emit();
    throw error;
  }
  return response.body;
}

export const api = {
  // note marks a request in the panel of the mock, such as "workaround" for
  // a request that the console sends only because the API lacks a better one.
  get: (path, note) => send('GET', path, undefined, note),
  post: (path, body) => send('POST', path, body),
  put: (path, body) => send('PUT', path, body),
  patch: (path, body) => send('PATCH', path, body),
  delete: (path) => send('DELETE', path),

  // events follows the events of a turn: GET /v1/turns/{id}/events. It calls
  // onEvent({id, type, data}) for each event, from the first one, and onEnd()
  // after the last event of the turn. If the connection breaks, it connects
  // again with Last-Event-ID and continues after the last event that it
  // received. It returns a function that stops following.
  events(turnID, {onEvent, onEnd = () => {}, onError = () => {}}) {
    const path = `/v1/turns/${turnID}/events`;
    let after = '';
    let stop = () => {};
    const following = {cut: () => { stop('connection lost'); connect(); }};
    function connect() {
      const entry = record({at: Date.now(), method: 'SSE', path, status: 0, ms: 0, flag: '', note: after ? 'Last-Event-ID: ' + after : '', events: 0, last: ''});
      const stream = open(turnID, {token, after}, (event) => {
        if (event.id) after = event.id;
        entry.events++;
        entry.last = event.type;
        if (!EVENTS.has(event.type)) entry.flag = 'planned';
        touch();
        onEvent({id: event.id, type: event.type, data: JSON.parse(event.data)});
      }, () => {
        entry.closed = true;
        streams.delete(following);
        touch();
        onEnd();
      });
      entry.status = stream.response.status;
      touch();
      if (stream.response.status !== 200) {
        const error = new ApiError(stream.response.status, stream.response.body, stream.response.headers);
        streams.delete(following);
        if (error.status === 401) unauthenticated.emit();
        queueMicrotask(() => onError(error));
        return;
      }
      stop = (why) => {
        if (!entry.closed) { entry.closed = true; entry.note = [entry.note, why].filter(Boolean).join(' · '); touch(); }
        stream.close();
      };
    }
    streams.add(following);
    connect();
    return () => {
      streams.delete(following);
      stop('closed by the page');
    };
  },

  // changes follows what changes in a project: GET /v1/projects/{id}/events.
  // It calls onSession(session) with every session of the project that
  // changes and that the caller may read, as the session is after the change.
  // The stream does not repeat what happened before it opened, so onOpen()
  // is called each time it has opened: what was read before that may be out
  // of date. If the connection breaks, it connects again. It returns a
  // function that stops following.
  changes(projectID, {onSession, onOpen = () => {}, onError = () => {}}) {
    const path = `/v1/projects/${projectID}/events`;
    let stop = () => {};
    const following = {cut: () => { stop('connection lost'); connect(); }};
    function connect() {
      const entry = record({at: Date.now(), method: 'SSE', path, status: 0, ms: 0, flag: flagOf('GET', path), note: '', events: 0, last: ''});
      const stream = watch(projectID, {token}, (event) => {
        entry.events++;
        entry.last = event.type;
        touch();
        if (event.type === 'session') onSession(JSON.parse(event.data));
      });
      entry.status = stream.response.status;
      touch();
      if (stream.response.status !== 200) {
        const error = new ApiError(stream.response.status, stream.response.body, stream.response.headers);
        entry.closed = true;
        streams.delete(following);
        if (error.status === 401) unauthenticated.emit();
        queueMicrotask(() => onError(error));
        return;
      }
      stop = (why) => {
        if (!entry.closed) { entry.closed = true; entry.note = why; touch(); }
        stream.close();
      };
      queueMicrotask(onOpen);
    }
    streams.add(following);
    connect();
    return () => {
      streams.delete(following);
      stop('closed by the page');
    };
  },
};

// The events of the API (README.md, "Turn events"). The panel of the mock marks
// a stream that carried any other event.
const EVENTS = new Set(['turn.queued', 'turn.started', 'message.delta', 'thought.delta', 'tool.call', 'tool.update', 'plan.update',
  'approval.requested', 'approval.resolved', 'turn.completed', 'turn.failed', 'turn.cancelled', 'turn.expired']);

// The event streams that are open, so that the mock can break them.
const streams = new Set();

// cutStreams breaks the connections of the open event streams, as a network
// would, and returns how many it broke. Each connects again.
export function cutStreams() {
  const open = [...streams];
  for (const following of open) following.cut();
  return open.length;
}

// installation returns what the console is configured with, which in the
// product comes from the server that serves it: whether the installation has
// an identity provider to sign in with, and the host that it shows.
export function installation() {
  return settings();
}
