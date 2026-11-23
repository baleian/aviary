import { useState } from "react";
import { api } from "../../api/client.ts";
import { keys, type ProjectContext } from "../../api/project.ts";
import type {
  McpServer,
  McpServerTools,
  Tool,
  Transport,
} from "../../api/types.ts";
import { cls } from "../../lib/cls.ts";
import { named, text, Wrong } from "../../lib/form.ts";
import { label, plural, sentence, transportName } from "../../lib/format.ts";
import { queryClient } from "../../query.ts";
import { Dialog } from "../../ui/Dialog.tsx";
import { Field, NameAndSlug } from "../../ui/fields.tsx";
import { Icon } from "../../ui/Icon.tsx";
import { Segmented } from "../../ui/Segmented.tsx";
import { toast } from "../../ui/toast.ts";
import { type HeaderRow, readHeaders, rowsOf } from "./rows.ts";
import { Headers } from "./Headers.tsx";

// said puts what came of asking a server for its tools in words, as a test
// of the connection to it.
function said(
  asked: McpServerTools,
  transport: Transport,
): { tone: "ok" | "bad" | ""; text: string; tools?: Tool[] } {
  if (!asked.reached) {
    return { tone: "bad", text: sentence("Not reached: " + asked.reason) };
  }
  const status = asked.status ?? 0;
  if (status >= 200 && status < 300) {
    const tools = asked.tools ?? [];
    return {
      tone: "ok",
      text: `Connected in ${asked.duration_ms} ms · ${plural(tools.length, "tool")}`,
      tools,
    };
  }
  if (status === 401 || status === 403) {
    return { tone: "bad", text: `The server refused the request (${status}).` };
  }
  if (status === 404 || status === 405) {
    const other = transportName(transport === "http" ? "sse" : "http");
    return {
      tone: "bad",
      text: `No MCP server answers ${transportName(transport)} at this URL (${status}). Try ${other}?`,
    };
  }
  return { tone: "bad", text: `The server answered ${status}.` };
}

// EditMcpServer is the dialog that adds an MCP server to a project, or
// replaces one: all of it but its slug. The secrets that it stores stay
// while its URL does. The connection can be tested before it is saved, by
// asking the server for its tools.
export function EditMcpServer({
  context,
  existing,
  onClose,
}: {
  context: ProjectContext;
  existing?: McpServer;
  onClose: () => void;
}) {
  const project = context.project.id;
  const [transport, setTransport] = useState<Transport>(
    existing?.transport ?? "http",
  );
  const [url, setUrl] = useState(existing?.url ?? "");
  const [rows, setRows] = useState<HeaderRow[]>(() =>
    rowsOf(existing?.headers),
  );
  const [trying, setTrying] = useState(false);
  const [tried, setTried] = useState<ReturnType<typeof said>>();
  const keeps = !!existing && url.trim() === existing.url;

  // endpoint reads what the form holds of how the server is reached.
  function endpoint(data: FormData): Record<string, unknown> {
    const at = text(data, "url");
    if (!at) throw new Wrong("Enter the URL of the MCP server.");
    return { url: at, transport, headers: readHeaders(data, rows, keeps) };
  }

  async function save(data: FormData): Promise<McpServer> {
    const body = {
      ...(existing ? { name: text(data, "name") } : named(data)),
      ...endpoint(data),
    };
    const saved = existing
      ? await api.put<McpServer>("/v1/mcp-servers/" + existing.id, body)
      : await api.post<McpServer>(`/v1/projects/${project}/mcp-servers`, body);
    await queryClient.invalidateQueries({ queryKey: keys.mcpServers(project) });
    return saved;
  }

  async function tryIt(form: HTMLFormElement) {
    setTried(undefined);
    setTrying(true);
    try {
      const asked = endpoint(new FormData(form));
      if (existing) asked.server = existing.id;
      const path = `/v1/projects/${project}/mcp-servers/tools`;
      setTried(said(await api.post<McpServerTools>(path, asked), transport));
    } catch (failure) {
      const why = failure instanceof Error ? failure.message : "";
      setTried({ tone: "bad", text: sentence(why) });
    } finally {
      setTrying(false);
    }
  }

  return (
    <Dialog
      size="wide"
      title={existing ? "Edit " + label(existing) : "Add an MCP server"}
      action={existing ? "Save server" : "Add server"}
      onSubmit={save}
      onClose={(saved) => {
        onClose();
        if (saved)
          toast(`${existing ? "Saved" : "Added"} ${label(saved)}`, "ok");
      }}
    >
      {existing ? (
        <div className="fld-row">
          <Field label="Name" optional>
            <input
              className="inp"
              name="name"
              defaultValue={existing.name}
              placeholder="Issue tracker"
              autoComplete="off"
              maxLength={120}
              data-autofocus
            />
          </Field>
          <Field label="Slug">
            <input
              className="inp mono"
              value={existing.slug}
              disabled
              readOnly
            />
          </Field>
        </div>
      ) : (
        <NameAndSlug example="Issue tracker" />
      )}
      <div className="fld-row url">
        <Field label="URL">
          <input
            className="inp mono"
            name="url"
            value={url}
            onChange={(event) => {
              setUrl(event.target.value);
              setTried(undefined);
            }}
            placeholder={
              transport === "sse"
                ? "https://mcp.example.com/sse"
                : "https://mcp.example.com/mcp"
            }
            autoComplete="off"
            spellCheck={false}
          />
        </Field>
        <div className="fld">
          <span className="lab">Transport</span>
          <Segmented
            value={transport}
            onChange={(value) => {
              setTransport(value);
              setTried(undefined);
            }}
            choices={[
              ["http", transportName("http")],
              ["sse", transportName("sse")],
            ]}
          />
        </div>
      </div>
      <div className="fld">
        <span className="lab">
          Headers<em>optional</em>
        </span>
        <Headers
          context={context}
          rows={rows}
          onRows={setRows}
          keeps={!existing || keeps}
          fresh="stored"
          names={["authorization", "x-api-key"]}
          example={{ name: "authorization", value: "Bearer …" }}
        />
      </div>
      <div className="try">
        <button
          className="btn sec sm"
          type="button"
          disabled={trying}
          onClick={(event) => void tryIt(event.currentTarget.form!)}
        >
          {trying && <span className="ring" />}
          Test connection
        </button>
        {tried && (
          <span className={cls("try-r", tried.tone)} role="status">
            {tried.text}
          </span>
        )}
      </div>
      {tried?.tools && tried.tools.length > 0 && (
        <ul className="tools" aria-label="Tools">
          {tried.tools.map((tool) => (
            <li key={tool.name} title={tool.description}>
              <Icon name="wrench" />
              {tool.name}
            </li>
          ))}
        </ul>
      )}
    </Dialog>
  );
}
