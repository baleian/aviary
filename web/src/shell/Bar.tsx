import { setTheme, useTheme } from "../theme.ts";
import { Icon, Use } from "../ui/Icon.tsx";

// Bar is the bar at the top of the console.
export function Bar() {
  const { dark } = useTheme();
  return (
    <header className="nav">
      <a className="logo" href="/" title="Aviary">
        <Use symbol="logo" />
        Aviary
      </a>
      <div className="nav-r">
        <button
          className="ib"
          type="button"
          title={
            dark ? "Switch to the light theme" : "Switch to the dark theme"
          }
          onClick={() => setTheme(dark ? "light" : "dark")}
        >
          <Icon name={dark ? "sun" : "moon"} className="lg" />
        </button>
      </div>
    </header>
  );
}
