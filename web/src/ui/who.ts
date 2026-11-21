import type { Me } from "../api/types.ts";

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
