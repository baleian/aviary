import { useSyncExternalStore } from "react";
import { prefs } from "./lib/prefs.ts";

// What a person chooses: one of the two themes, or that of the system.
export type Theme = "system" | "light" | "dark";

const system = matchMedia("(prefers-color-scheme: dark)");
const listeners = new Set<() => void>();

function chosen(): Theme {
  const value = prefs.get("theme");
  return value === "light" || value === "dark" ? value : "system";
}

function dark(): boolean {
  const theme = chosen();
  return theme === "dark" || (theme === "system" && system.matches);
}

// apply sets the theme of the document, which the styles read.
// public/theme.js does the same before the first paint.
function apply() {
  document.documentElement.dataset.theme = dark() ? "dark" : "light";
  for (const listener of listeners) listener();
}

export function setTheme(theme: Theme) {
  prefs.set("theme", theme);
  apply();
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

// useTheme returns the chosen theme, and whether the page is dark now.
export function useTheme(): { theme: Theme; dark: boolean } {
  return {
    theme: useSyncExternalStore(subscribe, chosen),
    dark: useSyncExternalStore(subscribe, dark),
  };
}

system.addEventListener("change", apply);
apply();
