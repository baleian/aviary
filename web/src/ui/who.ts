import type { Me, Member } from "../api/types.ts";

// How to show a person. A person whom the console cannot name is not known,
// and shows as anyone.
export interface Who {
  name: string;
  known: boolean;
  // What picks the colour: the ID of the user.
  key?: string;
  title?: string;
}

// self returns how to show the signed-in caller.
export function self(me: Me): Who {
  const name = me.display_name || me.username || "You";
  return { name, known: true, key: me.id };
}

// person returns how to show the user with the ID. The API reports users by
// ID. The console knows the caller and the members of the project that are
// users, and nobody else: not an administrator who is no member, and not the
// people of a member group.
export function person(me: Me, members: Member[], id?: string): Who {
  if (!id) return { name: "Nobody", known: false };
  if (id === me.id) return self(me);
  const user = members.find((member) => member.user?.id === id)?.user;
  if (user) {
    return { name: user.display_name || user.username, known: true, key: id };
  }
  const short = "User " + id.slice(0, 8);
  return { name: short, known: false, key: id, title: "User " + id };
}
