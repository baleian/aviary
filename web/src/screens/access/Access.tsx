import { useState } from "react";
import { api } from "../../api/client.ts";
import {
  ALLOWS,
  keys,
  mine,
  PERMISSIONS,
  type ProjectContext,
  ROLE_NOTES,
  ROLES,
  summary,
} from "../../api/project.ts";
import { useMe } from "../../api/queries.ts";
import type { Me, Member, Permission, Role } from "../../api/types.ts";
import { initials, label, plural } from "../../lib/format.ts";
import { useTitle } from "../../lib/title.ts";
import { queryClient } from "../../query.ts";
import { useProject } from "../../shell/project.ts";
import { AdminBadge, Avatar, RoleBadge } from "../../ui/Avatar.tsx";
import { Empty } from "../../ui/Blank.tsx";
import { Check } from "../../ui/fields.tsx";
import { Icon } from "../../ui/Icon.tsx";
import { Menu, MenuItem } from "../../ui/Menu.tsx";
import { Time } from "../../ui/Time.tsx";
import { oops, toast } from "../../ui/toast.ts";
import { nameOf, person } from "../../ui/who.ts";
import { AddMember, CustomPermissions, RemoveMember } from "./members.tsx";

// What a dialog of the screen is open for.
type Doing = { add: true } | { custom: Member } | { remove: Member } | null;

// Yours says how the caller is in the project.
function Yours({ context, me }: { context: ProjectContext; me: Me }) {
  if (me.admin) {
    return (
      <div className="card yours">
        <span
          className="av lg"
          style={{ background: "var(--badge)", color: "var(--canary)" }}
        >
          <Icon name="shield" className="lg" />
        </span>
        <div className="tx">
          <b>You are an administrator</b>
          <p>
            {me.id
              ? "You can do everything in every project."
              : "Without sign-in, everyone is. Members and roles do not apply."}
          </p>
        </div>
        <AdminBadge text="Administrator" />
      </div>
    );
  }
  const roles = new Set(mine(me, context.members).map((member) => member.role));
  return (
    <div className="card yours">
      <Avatar who={person(me, context.members, me.id)} size="lg" />
      <div className="tx">
        <b>Your access</b>
        <p>{summary(context.permissions)}</p>
      </div>
      <div style={{ display: "flex", gap: "6px" }}>
        {[...roles].map((role) => (
          <RoleBadge key={role} role={role} />
        ))}
      </div>
    </div>
  );
}

function MemberRow({
  context,
  me,
  member,
  onDo,
}: {
  context: ProjectContext;
  me: Me;
  member: Member;
  onDo: (doing: Doing) => void;
}) {
  const { members, roles } = context;
  const user = member.user;
  const permissions =
    member.role === "custom"
      ? (member.permissions ?? [])
      : (roles.find((role) => role.name === member.role)?.permissions ?? []);

  async function setRole(role: string) {
    try {
      await api.put("/v1/members/" + member.id, { role });
      await queryClient.invalidateQueries({
        queryKey: keys.members(context.project.id),
      });
    } catch (error) {
      oops(error);
    }
  }

  let face, who, kind;
  if (member.group) {
    const included = (me.groups ?? []).includes(member.group);
    face = (
      <span className="av grp lg">
        <Icon name="users" className="lg" />
      </span>
    );
    who = (
      <>
        <b className="mono" style={{ fontSize: "13.5px" }}>
          {member.group}
          {included && <span className="you">includes you</span>}
        </b>
        <small>Group</small>
      </>
    );
    kind = ["users", "Group"] as const;
  } else if (user?.id) {
    face = <Avatar who={person(me, members, user.id)} size="lg" />;
    who = (
      <>
        <b>
          {user.display_name || user.username}
          {user.id === me.id && <span className="you">you</span>}
        </b>
        <small>{user.username}</small>
      </>
    );
    kind = ["user", "User"] as const;
  } else {
    const username = user?.username ?? "";
    face = (
      <span className="av wait lg">{initials(username)[0].toLowerCase()}</span>
    );
    who = (
      <>
        <b>{username}</b>
        <small>Not signed in yet</small>
      </>
    );
    kind = ["clock", "Awaited"] as const;
  }

  return (
    <tr>
      <td>
        <div className="who">
          {face}
          <div>{who}</div>
        </div>
      </td>
      <td>
        <span className="kind">
          <Icon name={kind[0]} />
          {kind[1]}
        </span>
      </td>
      <td>
        {me.admin ? (
          <Menu
            button={(props) => (
              <button
                className="rsel"
                type="button"
                title="Change the role"
                {...props}
              >
                <RoleBadge role={member.role} />
                <Icon name="down" />
              </button>
            )}
          >
            <small className="h">Role of {nameOf(member)}</small>
            {ROLES.map((name) => (
              <MenuItem
                key={name}
                lead={<RoleBadge role={name} />}
                text=""
                small={ROLE_NOTES[name]}
                on={member.role === name}
                onClick={() => void setRole(name)}
              />
            ))}
            <hr />
            <MenuItem
              lead={<RoleBadge role="custom" />}
              text=""
              small="Custom permissions…"
              on={member.role === "custom"}
              onClick={() => onDo({ custom: member })}
            />
          </Menu>
        ) : (
          <RoleBadge role={member.role} />
        )}
      </td>
      <td className="can">
        {member.role === "custom"
          ? permissions.map((permission) => (
              <span key={permission} className="tag b">
                {permission}
              </span>
            ))
          : summary(permissions)}
      </td>
      <td>
        {me.admin && (
          <Menu
            align="right"
            button={(props) => (
              <button className="ib" type="button" title="More" {...props}>
                <Icon name="dots" className="lg" />
              </button>
            )}
          >
            <MenuItem
              glyph="set"
              text="Custom permissions…"
              onClick={() => onDo({ custom: member })}
            />
            <hr />
            <MenuItem
              glyph="trash"
              text="Remove from the project"
              danger
              onClick={() => onDo({ remove: member })}
            />
          </Menu>
        )}
      </td>
    </tr>
  );
}

// changed lists the permissions that a draft of a role gives or takes.
function changed(role: Role, draft: Permission[]): Permission[] {
  return PERMISSIONS.filter(
    (permission) =>
      draft.includes(permission) !== role.permissions.includes(permission),
  );
}

// RolesCard is what each role may do, the same in every project. An
// administrator ticks and clears its boxes, and then saves them together.
function RolesCard({ context, me }: { context: ProjectContext; me: Me }) {
  const { roles, members } = context;
  // The boxes that were ticked or cleared and not saved yet, by role.
  const [draft, setDraft] = useState<Record<string, Permission[]>>({});
  const [saving, setSaving] = useState(false);
  const drafted = (role: Role) => draft[role.name] ?? role.permissions;
  const count = roles.reduce(
    (total, role) => total + changed(role, drafted(role)).length,
    0,
  );
  const newest = roles.reduce<Role | undefined>(
    (latest, role) =>
      !latest || (role.updated_at ?? "") > (latest.updated_at ?? "")
        ? role
        : latest,
    undefined,
  );

  function tick(role: Role, permission: Permission, on: boolean) {
    const set = new Set(drafted(role));
    if (on) set.add(permission);
    else set.delete(permission);
    // Controlling every session includes seeing them.
    if (set.has("sessions.control_all")) set.add("sessions.view_all");
    const next = PERMISSIONS.filter((each) => set.has(each));
    setDraft({ ...draft, [role.name]: next });
  }

  async function save() {
    setSaving(true);
    try {
      for (const role of roles) {
        if (!changed(role, drafted(role)).length) continue;
        await api.put("/v1/roles/" + role.name, {
          permissions: drafted(role),
        });
      }
      await queryClient.invalidateQueries({ queryKey: keys.roles });
      setDraft({});
      toast("Roles saved", "ok");
    } catch (error) {
      oops(error);
    }
    setSaving(false);
  }

  return (
    <div className="card sec">
      <div className="tb-h">
        <div>
          <h2 className="h2">Roles</h2>
          <p className="sub sm">
            The same in every project.
            {newest?.updated_by && newest.updated_at && (
              <>
                {" "}
                Last changed <Time value={newest.updated_at} /> by{" "}
                {person(me, members, newest.updated_by).name}.
              </>
            )}
          </p>
        </div>
      </div>
      <table className="tb mx">
        <thead>
          <tr>
            <th>Role</th>
            {PERMISSIONS.map((permission) => (
              <th key={permission}>
                <b>{ALLOWS[permission]}</b>
                <code>{permission}</code>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {roles.map((role) => (
            <tr key={role.name}>
              <td>
                <RoleBadge role={role.name} />
                <small>{ROLE_NOTES[role.name] ?? ""}</small>
              </td>
              {PERMISSIONS.map((permission) => {
                const held = drafted(role).includes(permission);
                if (!me.admin) {
                  return (
                    <td key={permission}>
                      {held ? (
                        <span className="yes" title="Allowed">
                          <Icon name="check" className="lg" />
                        </span>
                      ) : (
                        <span className="no" title="Not allowed" />
                      )}
                    </td>
                  );
                }
                const implied =
                  permission === "sessions.view_all" &&
                  drafted(role).includes("sessions.control_all");
                const differs = held !== role.permissions.includes(permission);
                return (
                  <td key={permission} className={differs ? "chg" : undefined}>
                    <Check
                      checked={held}
                      disabled={permission === "project.view" || implied}
                      aria-label={`${role.name}: ${ALLOWS[permission]}`}
                      onChange={(event) =>
                        tick(role, permission, event.target.checked)
                      }
                    />
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
      <div className="tb-f">
        <span>
          {count ? (
            <span className="dirty">{plural(count, "unsaved change")}</span>
          ) : me.admin ? (
            ""
          ) : (
            "Only administrators can change roles."
          )}
        </span>
        {me.admin && (
          <div className="end">
            <button
              className="btn grey"
              type="button"
              disabled={!count || saving}
              onClick={() => setDraft({})}
            >
              Discard
            </button>
            <button
              className="btn pri"
              type="button"
              disabled={!count || saving}
              onClick={() => void save()}
            >
              Save changes
            </button>
          </div>
        )}
      </div>
    </div>
  );
}

// Access is the screen of who is in a project, and what each role may do.
export function Access() {
  const context = useProject();
  const me = useMe().data!;
  const { project, members } = context;
  const [filter, setFilter] = useState("");
  const [doing, setDoing] = useState<Doing>(null);
  useTitle("Access", label(project));

  const sought = filter.trim().toLowerCase();
  const shown = members.filter(
    (member) =>
      !sought ||
      [
        member.group,
        member.user?.display_name,
        member.user?.username,
        member.role,
      ].some((text) => text?.toLowerCase().includes(sought)),
  );

  return (
    <section className="page">
      <div className="wrap">
        <div className="pg-h">
          <div>
            <p className="over">{label(project)}</p>
            <h1 className="h1">Access</h1>
          </div>
        </div>
        <Yours context={context} me={me} />

        <div className="card">
          <div className="tb-h">
            <h2 className="h2">Members</h2>
            <span className="cnt">{members.length}</span>
            <div className="end">
              {members.length > 4 && (
                <label className="inp-i">
                  <Icon name="search" />
                  <input
                    className="inp"
                    placeholder="Find a member"
                    aria-label="Find a member"
                    value={filter}
                    onChange={(event) => setFilter(event.target.value)}
                  />
                </label>
              )}
              {me.admin && (
                <button
                  className="btn pri"
                  type="button"
                  onClick={() => setDoing({ add: true })}
                >
                  <Icon name="plus" />
                  Add member
                </button>
              )}
            </div>
          </div>
          {members.length ? (
            <table className="tb">
              <thead>
                <tr>
                  <th style={{ width: "30%" }}>Member</th>
                  <th style={{ width: "13%" }}>Kind</th>
                  <th style={{ width: "17%" }}>Role</th>
                  <th>What they may do</th>
                  <th style={{ width: "48px" }} />
                </tr>
              </thead>
              <tbody>
                {shown.map((member) => (
                  <MemberRow
                    key={member.id}
                    context={context}
                    me={me}
                    member={member}
                    onDo={setDoing}
                  />
                ))}
                {!shown.length && (
                  <tr>
                    <td colSpan={5}>
                      <p className="sub" style={{ padding: "12px 8px" }}>
                        No member matches “{sought}”.
                      </p>
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          ) : (
            <Empty glyph="users" title="No members yet" />
          )}
          {!me.admin && (
            <div className="tb-f">
              <Icon name="info" />
              Only administrators can change members.
            </div>
          )}
        </div>

        <RolesCard context={context} me={me} />
      </div>

      {doing && "add" in doing && (
        <AddMember context={context} onClose={() => setDoing(null)} />
      )}
      {doing && "custom" in doing && (
        <CustomPermissions
          context={context}
          member={doing.custom}
          onClose={() => setDoing(null)}
        />
      )}
      {doing && "remove" in doing && (
        <RemoveMember
          context={context}
          member={doing.remove}
          onClose={() => setDoing(null)}
        />
      )}
    </section>
  );
}
