/**
 * Theme handling — data-theme on <html>, persisted to localStorage.
 * Default: dark.
 */
"use client";

import * as React from "react";

type Theme = "light" | "dark";
const STORAGE_KEY = "aviary-theme";

export function useTheme() {
  const [theme, setThemeState] = React.useState<Theme>("dark");

  // Hydrate from localStorage on mount (client-only)
  React.useEffect(() => {
    const stored = (localStorage.getItem(STORAGE_KEY) as Theme) || "dark";
    setThemeState(stored);
    document.documentElement.setAttribute("data-theme", stored);
  }, []);

  const setTheme = React.useCallback((next: Theme) => {
    setThemeState(next);
    document.documentElement.setAttribute("data-theme", next);
    localStorage.setItem(STORAGE_KEY, next);
  }, []);

  return { theme, setTheme };
}

/**
 * Blocking inline script — prevents theme flash on initial paint.
 * Put inside <head> of app/layout.tsx, BEFORE <body>:
 *
 *   <script dangerouslySetInnerHTML={{ __html: THEME_SCRIPT }} />
 */
export const THEME_SCRIPT = `
(function() {
  try {
    var t = localStorage.getItem("${STORAGE_KEY}") || "dark";
    document.documentElement.setAttribute("data-theme", t);
  } catch (e) {
    document.documentElement.setAttribute("data-theme", "dark");
  }
})();
`;
