// Small settings that the browser keeps, such as the theme. Storage can be
// unavailable, and then a setting lasts until the page is closed.
const memory = new Map<string, string>();

export const prefs = {
  get(key: string): string | null {
    try {
      const value = localStorage.getItem("aviary." + key);
      if (value != null) return value;
    } catch {
      // The setting is in memory, if anywhere.
    }
    return memory.get(key) ?? null;
  },

  set(key: string, value: string | null) {
    if (value == null) memory.delete(key);
    else memory.set(key, value);
    try {
      if (value == null) localStorage.removeItem("aviary." + key);
      else localStorage.setItem("aviary." + key, value);
    } catch {
      // The setting stays in memory.
    }
  },
};
