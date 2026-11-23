// The headers of an endpoint in its dialog: what each row holds, and what
// the API is sent.

import type { Header, Secret as Kept } from "../../api/types.ts";
import { text, Wrong } from "../../lib/form.ts";

// What the value of a header is: text that members of the project can see,
// a secret that Aviary keeps, or a secret in a secret store of the project.
export type Source = "value" | "stored" | "store";

// A header of a form, as its row has it. A row that holds a secret that
// Aviary keeps shows that it does, and keeps it unless it is replaced.
export interface HeaderRow {
  id: number;
  name: string;
  source: Source;
  value: string;
  at?: { store: string; path: string; key: string };
  stored?: boolean;
  replacing?: boolean;
  // A row that was added takes the focus.
  added?: boolean;
}

// rowsOf returns the rows of the headers of an endpoint.
export function rowsOf(headers: Header[] = []): HeaderRow[] {
  return headers.map((header, id) => {
    const secret = header.secret;
    if (secret && "store" in secret) {
      return { id, name: header.name, source: "store", value: "", at: secret };
    }
    if (secret) {
      return {
        id,
        name: header.name,
        source: "stored",
        value: "",
        stored: true,
      };
    }
    return {
      id,
      name: header.name,
      source: "value",
      value: header.value ?? "",
    };
  });
}

// readHeaders reads the headers of a form, as the API takes them. A row that
// is left empty is no header. keeps says whether the secrets that are stored
// stay: they do while the URL is the one that they were stored for.
export function readHeaders(
  data: FormData,
  rows: HeaderRow[],
  keeps: boolean,
): { name: string; value?: string; secret?: Kept | { value: string } }[] {
  const headers = [];
  for (const row of rows) {
    const field = (part: string) => text(data, `h${row.id}-${part}`);
    const name = field("name");
    const kept =
      row.source === "stored" && row.stored && keeps && !row.replacing;
    const given =
      row.source === "store"
        ? field("path") || field("key")
        : kept || field("value");
    if (!name && !given) continue;
    if (!name) throw new Wrong("Enter the name of each header.");
    if (row.source === "value") {
      if (!field("value")) throw new Wrong(`Enter the value of ${name}.`);
      headers.push({ name, value: field("value") });
    } else if (row.source === "stored") {
      if (kept) {
        headers.push({ name, secret: { stored: true as const } });
        continue;
      }
      if (!field("value")) {
        throw new Wrong(
          row.stored
            ? `Enter the value of ${name} again.`
            : `Enter the value of ${name}.`,
        );
      }
      headers.push({ name, secret: { value: field("value") } });
    } else {
      if (!field("store")) throw new Wrong("Register a secret store first.");
      if (!field("path") || !field("key")) {
        throw new Wrong(`Enter the path and the key of the secret of ${name}.`);
      }
      headers.push({
        name,
        secret: {
          store: field("store"),
          path: field("path"),
          key: field("key"),
        },
      });
    }
  }
  return headers;
}
