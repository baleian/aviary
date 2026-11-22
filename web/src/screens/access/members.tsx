import { useState } from "react";
import { api } from "../../api/client.ts";
import {
  ALLOWS,
  keys,
  PERMISSIONS,
  type ProjectContext,
  ROLE_NOTES,
  ROLES,
} from "../../api/project.ts";
import type { Member, Permission } from "../../api/types.ts";
import { text, Wrong } from "../../lib/form.ts";
import { queryClient } from "../../query.ts";
import { Dialog } from "../../ui/Dialog.tsx";
import { Check, Field, Select } from "../../ui/fields.tsx";
import { Segmented } from "../../ui/Segmented.tsx";
import { toast } from "../../ui/toast.ts";
import { nameOf } from "../../ui/who.ts";

// held returns the permissions that a set of ticked boxes stands for: seeing
// the project is always among them, and controlling every session includes
// seeing them.
function held(ticked: Permission[]): Permission[] {
  const set = new Set<Permission>([...ticked, "project.view"]);
  if (set.has("sessions.control_all")) set.add("sessions.view_all");
  return PERMISSIONS.filter((permission) => set.has(permission));
}

// PermissionBoxes is the list of the permissions, with a box each.
function PermissionBoxes({
  value,
  onChange,
}: {
  value: Permission[];
  onChange: (value: Permission[]) => void;
}) {
  const controls = value.includes("sessions.control_all");
  return (
    <div className="perms">
      {PERMISSIONS.map((permission) => (
        <Check
          key={permission}
          checked={value.includes(permission)}
          disabled={
            permission === "project.view" ||
            (permission === "sessions.view_all" && controls)
          }
          onChange={(event) =>
            onChange(
              held(
                event.target.checked
                  ? [...value, permission]
                  : value.filter((other) => other !== permission),
              ),
            )
          }
        >
          <b>
            {ALLOWS[permission]} <span className="tag">{permission}</span>
          </b>
        </Check>
      ))}
    </div>
  );
}

// AddMember is the dialog that adds a person or a group to a project, with
// a role or with permissions of its own.
export function AddMember({
  context,
  onClose,
}: {
  context: ProjectContext;
  onClose: () => void;
}) {
  const [kind, setKind] = useState<"user" | "group">("user");
  const [role, setRole] = useState("user");
  const [custom, setCustom] = useState<Permission[]>(held(["sessions.run"]));
  const project = context.project.id;
  return (
    <Dialog
      title="Add a member"
      action="Add member"
      onSubmit={async (data) => {
        const who = text(data, kind);
        if (!who) {
          throw new Wrong(
            kind === "user" ? "Enter a username." : "Enter a group.",
          );
        }
        const grant = role === "custom" ? { permissions: custom } : { role };
        const added = await api.post<Member>(
          `/v1/projects/${project}/members`,
          { [kind]: who, ...grant },
        );
        await queryClient.invalidateQueries({
          queryKey: keys.members(project),
        });
        return added;
      }}
      onClose={(added) => {
        onClose();
        if (added) toast("Added " + nameOf(added), "ok");
      }}
    >
      <div className="fld">
        <Segmented
          size="lg"
          choices={[
            ["user", "A person", "user"],
            ["group", "A group", "users"],
          ]}
          value={kind}
          onChange={setKind}
        />
      </div>
      <div className="fld">
        {kind === "user" ? (
          <Field label="Username">
            <input
              className="inp"
              name="user"
              placeholder="dana@example.com"
              autoComplete="off"
              spellCheck={false}
              data-autofocus
            />
          </Field>
        ) : (
          <Field label="Group">
            <input
              className="inp mono"
              name="group"
              placeholder="team-a"
              autoComplete="off"
              spellCheck={false}
            />
          </Field>
        )}
      </div>
      <Field label="Role">
        <Select
          value={role}
          onChange={(event) => setRole(event.target.value)}
          options={[
            ...ROLES.map((name): [string, string] => [
              name,
              `${name}: ${ROLE_NOTES[name].toLowerCase()}`,
            ]),
            ["custom", "Custom permissions…"],
          ]}
        />
      </Field>
      {role === "custom" && (
        <div className="fld">
          <PermissionBoxes value={custom} onChange={setCustom} />
        </div>
      )}
    </Dialog>
  );
}

// CustomPermissions is the dialog that gives a member permissions of its
// own, in place of a role.
export function CustomPermissions({
  context,
  member,
  onClose,
}: {
  context: ProjectContext;
  member: Member;
  onClose: () => void;
}) {
  const role = context.roles.find((other) => other.name === member.role);
  const [value, setValue] = useState<Permission[]>(
    held(
      (member.role === "custom" ? member.permissions : role?.permissions) ?? [],
    ),
  );
  return (
    <Dialog
      title={"Permissions of " + nameOf(member)}
      action="Save permissions"
      onSubmit={async () => {
        await api.put("/v1/members/" + member.id, { permissions: value });
        await queryClient.invalidateQueries({
          queryKey: keys.members(context.project.id),
        });
        return true;
      }}
      onClose={onClose}
    >
      <PermissionBoxes value={value} onChange={setValue} />
    </Dialog>
  );
}

// RemoveMember asks whether to remove a member from a project, and does.
export function RemoveMember({
  context,
  member,
  onClose,
}: {
  context: ProjectContext;
  member: Member;
  onClose: () => void;
}) {
  return (
    <Dialog
      size="slim"
      danger
      title={`Remove ${nameOf(member)}?`}
      lead={
        member.group
          ? "Its members lose the access that it gives them."
          : "They lose access to this project."
      }
      action="Remove member"
      onSubmit={async () => {
        await api.delete("/v1/members/" + member.id);
        await queryClient.invalidateQueries({
          queryKey: keys.members(context.project.id),
        });
        return true;
      }}
      onClose={(removed) => {
        onClose();
        if (removed) toast(`Removed ${nameOf(member)}`, "ok");
      }}
    />
  );
}
