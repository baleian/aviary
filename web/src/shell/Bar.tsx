import { Link } from "react-router";
import type { Me } from "../api/types.ts";
import { signOut } from "../session/auth.ts";
import { setTheme, type Theme, useTheme } from "../theme.ts";
import { AdminBadge, Avatar } from "../ui/Avatar.tsx";
import { Icon, Use } from "../ui/Icon.tsx";
import type { IconName } from "../ui/icons.ts";
import { Menu, MenuItem } from "../ui/Menu.tsx";
import { Segmented } from "../ui/Segmented.tsx";
import { self } from "../ui/who.ts";

const THEMES: [Theme, string, IconName][] = [
  ["system", "Auto", "monitor"],
  ["light", "Light", "sun"],
  ["dark", "Dark", "moon"],
];

function ThemeRow() {
  const { theme } = useTheme();
  return (
    <div className="row">
      Theme
      <Segmented choices={THEMES} value={theme} onChange={setTheme} />
    </div>
  );
}

// Account is the button of the caller, and the menu that it opens.
function Account({ me }: { me: Me }) {
  // Without an identity provider nobody signs in, and the caller has no ID.
  if (!me.id) {
    return (
      <Menu
        align="right"
        className="acct"
        button={(props) => (
          <button className="me open" type="button" {...props}>
            <Icon name="user" />
            No sign-in
            <Icon name="down" />
          </button>
        )}
      >
        <div className="who">
          <div>
            <b>No sign-in</b>
            <small>Everyone is an administrator here.</small>
          </div>
        </div>
        <hr />
        <ThemeRow />
      </Menu>
    );
  }
  const who = self(me);
  return (
    <Menu
      align="right"
      className="acct"
      button={(props) => (
        <button className="me" type="button" {...props}>
          <Avatar who={who} size="sm" />
          <span className="nm">{who.name}</span>
          {me.admin && <AdminBadge />}
          <Icon name="down" />
        </button>
      )}
    >
      <div className="who">
        <Avatar who={who} size="lg" />
        <div>
          <b>{who.name}</b>
          <small>{me.username || me.subject}</small>
        </div>
      </div>
      <hr />
      <MenuItem glyph="user" text="Your account" to="/me" />
      <ThemeRow />
      <hr />
      <MenuItem glyph="logout" text="Sign out" onClick={signOut} />
    </Menu>
  );
}

// Bar is the bar at the top of every signed-in screen.
export function Bar({ me }: { me: Me }) {
  const { dark } = useTheme();
  return (
    <header className="nav">
      <Link className="logo" to="/" title="Aviary">
        <Use symbol="logo" />
        Aviary
      </Link>
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
        <Account me={me} />
      </div>
    </header>
  );
}
