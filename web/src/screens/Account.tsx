import type { ReactNode } from "react";
import { useMe } from "../api/queries.ts";
import type { Me } from "../api/types.ts";
import { useTitle } from "../lib/title.ts";
import { signOut } from "../session/auth.ts";
import { AdminBadge, Avatar } from "../ui/Avatar.tsx";
import { CopyButton } from "../ui/CopyButton.tsx";
import { Icon } from "../ui/Icon.tsx";
import { self } from "../ui/who.ts";

const none = <span className="sub">None</span>;

function copied(value: string | undefined, what: string): ReactNode {
  if (!value) return none;
  return (
    <div className="idb">
      <span>{value}</span>
      <CopyButton text={value} what={what} />
    </div>
  );
}

// Identity is the table of what the installation read from the caller's
// token.
export function Identity({ me }: { me: Me }) {
  const groups = me.groups ?? [];
  return (
    <dl className="kv wide">
      <dt>User ID</dt>
      <dd>{copied(me.id, "user ID")}</dd>
      <dt>Subject</dt>
      <dd>{copied(me.subject, "subject")}</dd>
      <dt>Username</dt>
      <dd>{me.username || none}</dd>
      <dt>Display name</dt>
      <dd>{me.display_name || none}</dd>
      <dt>Groups</dt>
      <dd className="top">
        {groups.length ? (
          <div className="groups">
            {groups.map((group) => (
              <span key={group} className="tag">
                {group}
              </span>
            ))}
          </div>
        ) : (
          none
        )}
      </dd>
      <dt>Administrator</dt>
      <dd>{me.admin ? "Yes" : "No"}</dd>
    </dl>
  );
}

// Account is the screen of the caller: who the installation takes them for.
export function Account() {
  const me = useMe().data;
  useTitle("Your account");
  if (!me) return null;
  if (!me.id) {
    return (
      <section className="page">
        <div className="wrap narrow">
          <div className="card acct-card">
            <div
              className="acct-top"
              style={{ border: 0, margin: 0, padding: 0 }}
            >
              <span className="av xl anon">
                <Icon name="user" className="xl" />
              </span>
              <div>
                <h1>No sign-in</h1>
                <p>Everyone is an administrator here.</p>
              </div>
            </div>
          </div>
        </div>
      </section>
    );
  }
  const who = self(me);
  return (
    <section className="page">
      <div className="wrap narrow">
        <div className="card acct-card">
          <div className="acct-top">
            <Avatar who={who} size="xl" />
            <div>
              <h1>
                {who.name}
                {me.admin && <AdminBadge text="Administrator" />}
              </h1>
              <p>{me.username || me.subject}</p>
            </div>
            <button className="btn sec" type="button" onClick={signOut}>
              <Icon name="logout" />
              Sign out
            </button>
          </div>
          <Identity me={me} />
        </div>
      </div>
    </section>
  );
}
