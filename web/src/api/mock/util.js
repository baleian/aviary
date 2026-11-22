// Small helpers of the mock.

// uuid7 returns a UUID of version 7 for a time. IDs made later sort later.
let lastStamp = 0;
let sequence = 0;
export function uuid7(ms = Date.now(), random = Math.random) {
  if (ms === lastStamp) sequence++;
  else { lastStamp = ms; sequence = 0; }
  const hex = (n, width) => Math.floor(n).toString(16).padStart(width, '0').slice(-width);
  const stamp = hex(ms, 12);
  // The IDs of one millisecond are told apart by their place in it, which
  // comes before what is random.
  const a = hex(sequence * 0x10 + random() * 0x10, 3);
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

export function copy(value) {
  return JSON.parse(JSON.stringify(value));
}
