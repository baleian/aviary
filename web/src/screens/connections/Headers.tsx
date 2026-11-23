import type { ProjectContext } from "../../api/project.ts";
import { Secret, Select } from "../../ui/fields.tsx";
import { Icon } from "../../ui/Icon.tsx";
import type { HeaderRow, Source } from "./rows.ts";

// Headers is the list of the headers of an endpoint in its dialog: a name
// for each, and where its value comes from. fresh is where the value of a
// row that is added comes from, and names are the names to suggest.
export function Headers({
  context,
  rows,
  onRows,
  keeps,
  fresh,
  names = [],
  example,
}: {
  context: ProjectContext;
  rows: HeaderRow[];
  onRows: (rows: HeaderRow[]) => void;
  keeps: boolean;
  fresh: Source;
  names?: string[];
  example: { name: string; value: string };
}) {
  const stores = context.stores;
  const change = (row: HeaderRow, to: Partial<HeaderRow>) =>
    onRows(
      rows.map((other) => (other.id === row.id ? { ...row, ...to } : other)),
    );
  const list = names.length ? "header-names" : undefined;
  return (
    <>
      {rows.length > 0 && (
        <div className="hdrs" role="list">
          {rows.map((row) => (
            <div className="hdr" key={row.id} role="listitem">
              <input
                className="inp mono"
                name={`h${row.id}-name`}
                aria-label="Header name"
                defaultValue={row.name}
                placeholder={example.name}
                autoComplete="off"
                spellCheck={false}
                autoFocus={row.added}
                list={list}
              />
              <Select
                name={`h${row.id}-source`}
                aria-label="Where the value comes from"
                value={row.source}
                onChange={(event) =>
                  change(row, { source: event.target.value as Source })
                }
                options={[
                  ["value", "Plain text"],
                  ["stored", "Stored in Aviary"],
                  ["store", "In a secret store"],
                ]}
              />
              {row.source === "value" ? (
                <input
                  className="inp mono"
                  name={`h${row.id}-value`}
                  aria-label="Header value"
                  defaultValue={row.value}
                  placeholder={example.value}
                  autoComplete="off"
                  spellCheck={false}
                />
              ) : row.source === "stored" ? (
                row.stored && keeps && !row.replacing ? (
                  <span className="kept">
                    <Icon name="lock" />
                    Stored
                    <button
                      className="btn quiet sm"
                      type="button"
                      onClick={() => change(row, { replacing: true })}
                    >
                      Replace
                    </button>
                  </span>
                ) : (
                  <Secret
                    name={`h${row.id}-value`}
                    aria-label="Header value"
                    placeholder={row.stored ? "Enter it again" : "Value"}
                    autoFocus={row.replacing}
                  />
                )
              ) : stores.length ? (
                <Select
                  name={`h${row.id}-store`}
                  aria-label="Secret store"
                  defaultValue={row.at?.store}
                  options={stores.map((store) => [store.id, store.name])}
                />
              ) : (
                <span className="kept none">No secret store yet</span>
              )}
              <button
                className="ib sm"
                type="button"
                title="Remove header"
                onClick={() =>
                  onRows(rows.filter((other) => other.id !== row.id))
                }
              >
                <Icon name="x" />
              </button>
              {row.source === "store" && stores.length > 0 && (
                <div className="hdr-at">
                  <input
                    className="inp mono"
                    name={`h${row.id}-path`}
                    aria-label="Path of the secret"
                    defaultValue={row.at?.path}
                    placeholder="team/mcp"
                    autoComplete="off"
                    spellCheck={false}
                  />
                  <input
                    className="inp mono"
                    name={`h${row.id}-key`}
                    aria-label="Key of the secret"
                    defaultValue={row.at?.key}
                    placeholder="token"
                    autoComplete="off"
                    spellCheck={false}
                  />
                </div>
              )}
            </div>
          ))}
        </div>
      )}
      {list && (
        <datalist id={list}>
          {names.map((name) => (
            <option key={name} value={name} />
          ))}
        </datalist>
      )}
      <button
        className="btn quiet sm"
        type="button"
        onClick={() => {
          const id = rows.length ? rows[rows.length - 1].id + 1 : 0;
          onRows([
            ...rows,
            { id, name: "", source: fresh, value: "", added: true },
          ]);
        }}
      >
        <Icon name="plus" />
        Add header
      </button>
      {rows.some((row) => row.source === "value") && (
        <span className="hint">
          Members of the project can see plain text values.
        </span>
      )}
      {!keeps && rows.some((row) => row.stored && row.source === "stored") && (
        <span className="hint warn" role="status">
          <Icon name="warn" />
          <span>The URL changed: enter the stored values again.</span>
        </span>
      )}
    </>
  );
}
