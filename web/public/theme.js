// Sets the theme before the first paint, so that a dark page does not flash
// light. src/theme.ts keeps it from then on.
(function () {
  let chosen = "system";
  try {
    chosen = localStorage.getItem("aviary.theme") || "system";
  } catch {
    // Without storage the theme is that of the system.
  }
  const dark =
    chosen === "dark" ||
    (chosen === "system" && matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.dataset.theme = dark ? "dark" : "light";
})();
