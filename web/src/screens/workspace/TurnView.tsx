import { memo, type ReactNode } from "react";
import type { Tinted } from "../../api/queries.ts";
import type {
  Agent,
  Item,
  McpServer,
  PlanEntry,
  Turn,
} from "../../api/types.ts";
import { cls } from "../../lib/cls.ts";
import {
  clock,
  duration,
  label,
  num,
  transportName,
} from "../../lib/format.ts";
import { followed, type Status, STATUS_NAMES } from "../../lib/sessions.ts";
import { AgentTile, Avatar } from "../../ui/Avatar.tsx";
import { Chip } from "../../ui/Chip.tsx";
import { Icon } from "../../ui/Icon.tsx";
import type { IconName } from "../../ui/icons.ts";
import { Time } from "../../ui/Time.tsx";
import type { Who } from "../../ui/who.ts";
import { CodeBlock, Markdown } from "./Markdown.tsx";
import { Command, Request } from "./Request.tsx";
import {
  changed,
  exitCode,
  fieldsOf,
  findDiff,
  inputOf,
  outputText,
  pathOf,
  serverOf,
} from "./tools.ts";

type ToolItem = Extract<Item, { type: "tool_call" }>;
type ApprovalItem = Extract<Item, { type: "approval" }>;
type UnsupportedItem = Extract<Item, { type: "mcp_unsupported" }>;

const NONE: McpServer[] = [];

const AND = new Intl.ListFormat("en", { type: "conjunction" });
const OR = new Intl.ListFormat("en", { type: "disjunction" });

// The icon of each kind of tool call.
const KINDS: Record<string, IconName> = {
  read: "doc",
  edit: "edit",
  delete: "trash",
  move: "arrow",
  search: "search",
  execute: "term",
  think: "think",
  fetch: "globe",
};

// What each failure reason of the API means for the person who reads it.
const FAILURES: Record<string, string> = {
  harness_error: "The agent ran into an error",
  resume_failed: "The agent could not pick up the conversation",
  startup_timed_out: "The agent did not start in time",
  timed_out: "The turn took too long and was stopped",
  sandbox_lost: "The sandbox was lost, with the files in it",
  session_expired: "The session was unused for too long and has ended",
  runner_error: "Something went wrong",
};

const STOPS: Record<string, string> = {
  max_tokens: "Stopped at the output limit of the model.",
  max_turn_requests: "Stopped at the limit of model requests.",
  refusal: "The model refused to continue.",
  cancelled: "Ended because an action was refused.",
};

// What the agent is doing while its turn has not ended.
const DOING: Partial<Record<Status, string>> = {
  queued: "is in the queue",
  starting: "is starting",
  running: "is working",
  waiting: "is waiting for an answer",
  stopping: "is stopping",
};

function Plan({ entries, over }: { entries: PlanEntry[]; over: boolean }) {
  const done = entries.filter((entry) => entry.status === "completed").length;
  return (
    <div className="plan">
      <div className="plan-h">
        <Icon name="list" />
        Plan
        <span className="pips">
          {entries.map((entry, index) => (
            <i
              key={index}
              className={cls(
                entry.status === "completed" && "done",
                entry.status === "in_progress" && "now",
              )}
            />
          ))}
        </span>
        <small className={cls(done === entries.length && "all")}>
          {done} of {entries.length} done
        </small>
      </div>
      <ol>
        {entries.map((entry, index) => (
          <li key={index} className={entry.status}>
            {entry.status === "completed" ? (
              <span className="c-ok">
                <Icon name="ok" className="lg" />
              </span>
            ) : entry.status === "in_progress" && !over ? (
              <span className="ring" />
            ) : (
              <span className="todo" />
            )}
            <span>{entry.content}</span>
            {entry.priority === "high" && (
              <span className="pri">high priority</span>
            )}
          </li>
        ))}
      </ol>
    </div>
  );
}

// Answered is the line that says how an approval request ended. Alone, it
// also says what the request was about.
function Answered({
  item,
  alone,
  server,
  person,
}: {
  item: ApprovalItem;
  alone?: boolean;
  // The MCP server of the tool call, if it calls a tool of one.
  server?: string;
  person: (id?: string) => Who;
}) {
  const option = item.options?.find((other) => other.id === item.option_id);
  if (item.outcome !== "selected" || !option) {
    return (
      <p className={cls("answer no", alone && "alone")}>
        <Icon name="shield" />
        <span>Not answered</span>
      </p>
    );
  }
  const allowed = option.kind.startsWith("allow");
  return (
    <p className={cls("answer", !allowed && "no", alone && "alone")}>
      <Icon name="shield" />
      <span>
        {alone && item.tool_call?.mcp ? (
          <>
            <b>{item.tool_call.mcp.tool}</b> on {server}:{" "}
          </>
        ) : (
          alone &&
          item.tool_call?.title && (
            <>
              <b>{item.tool_call.title}</b>:{" "}
            </>
          )
        )}
        <b>{option.name}</b>
        {item.answered_by && <> by {person(item.answered_by).name}</>}
      </span>
    </p>
  );
}

// Step is a tool call: what was run or changed, and what came of it.
const Step = memo(function Step({
  item,
  approval,
  server,
  over,
  person,
}: {
  item: ToolItem;
  approval?: ApprovalItem;
  // The MCP server of the tool call, if it calls a tool of one.
  server?: string;
  over: boolean;
  person: (id?: string) => Who;
}) {
  const input = inputOf(item.input);
  const kind = item.kind || "other";
  // A tool of an MCP server runs on the server, and what it is given says
  // nothing of the workspace.
  const mcp = !!item.mcp;
  const diff =
    findDiff(item.output) ??
    findDiff(item.input) ??
    (mcp ? null : changed(input));
  const output = outputText(item.output);
  const code = exitCode(item.output);
  const failed = item.status === "failed";
  const raw = Object.fromEntries(
    (["input", "output", "meta"] as const)
      .filter((field) => item[field] !== undefined)
      .map((field) => [field, item[field]]),
  );
  const title = item.mcp?.tool ?? (item.title || kind);
  const command = !mcp && typeof input.command === "string";
  const path = mcp ? "" : pathOf(input);
  const fields = command ? [] : fieldsOf(input, mcp);
  // The code goes unsaid when the command succeeded and its output shows.
  const unsaid = code === 0 && !!output;
  const mark =
    item.status === "completed" ? (
      <span className="c-ok">
        <Icon name="ok" className="lg" />
      </span>
    ) : item.status === "failed" ? (
      <span className="c-fail">
        <Icon name="fail" className="lg" />
      </span>
    ) : over ? (
      <span className="c-grey" title="Did not finish">
        <Icon name="cancel" className="lg" />
      </span>
    ) : item.status === "in_progress" ? (
      <span className="ring" />
    ) : (
      <span className="c-grey" title="Pending">
        <Icon name="clock" className="lg" />
      </span>
    );
  return (
    <details className="step">
      <summary>
        <span className={cls("kd", approval && "y")}>
          <Icon
            name={
              approval
                ? "shield"
                : item.mcp
                  ? "plug"
                  : (KINDS[kind] ?? "wrench")
            }
          />
        </span>
        <span
          className={cls(
            "step-t",
            kind === "execute" && title === input.command && "cmdline",
          )}
        >
          {title}
        </span>
        {item.mcp ? (
          <span className="tag srv">{server}</span>
        ) : (
          <span className="tag">{kind}</span>
        )}
        {mark}
        <Icon name="chev" className="chev" />
      </summary>
      <div className="step-b">
        {approval && (
          <Answered item={approval} server={server} person={person} />
        )}
        {command ? (
          <Command input={input} exit={unsaid ? null : code} />
        ) : (
          path && (
            <p className="step-p">
              <Icon name="doc" />
              {path}
            </p>
          )
        )}
        {fields.length > 0 && (
          <dl className="step-f">
            {fields.map(([name, value]) => (
              <div key={name}>
                <dt>{name}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
        )}
        {diff && <CodeBlock text={diff} language="diff" bare={!!path} />}
        {/* What a tool said is its change, unless it failed. */}
        {output && (!diff || failed) && (
          <pre className={cls("code", failed && "bad")}>{output}</pre>
        )}
        {!command && !unsaid && code != null && (
          <p className="step-n">
            Exited with code <b>{code}</b>.
          </p>
        )}
        {failed && !output && <p className="step-n">The tool call failed.</p>}
        {Object.keys(raw).length > 0 && (
          <details className="raw">
            <summary>
              <Icon name="chev" className="chev" />
              Raw
            </summary>
            <pre className="code">{JSON.stringify(raw, null, 2)}</pre>
          </details>
        )}
      </div>
    </details>
  );
});

// Unsupported names the MCP servers that the turn ran without, since its
// harness does not support how they are reached.
function Unsupported({
  item,
  servers,
  harness,
}: {
  item: UnsupportedItem;
  servers: McpServer[];
  harness: string;
}) {
  const left = item.servers ?? [];
  if (!left.length) return null;
  const names = left.map((each) => {
    const server = servers.find((other) => other.id === each.id);
    return server ? label(server) : each.name;
  });
  const transports = [
    ...new Set(left.map((each) => transportName(each.transport))),
  ];
  return (
    <p className="state">
      <span className="c-wait">
        <Icon name="plug" className="lg" />
      </span>
      <span>
        {AND.formatToParts(names).map((part, index) =>
          part.type === "element" ? (
            <b key={index}>{part.value}</b>
          ) : (
            part.value
          ),
        )}{" "}
        {names.length === 1 ? "was" : "were"} left out: the {harness} harness
        does not support {OR.format(transports)} servers.
      </span>
    </p>
  );
}

// Blocks is what the agent said and did, in order. Tool calls that follow
// each other are one list of steps.
function Blocks({
  items,
  servers,
  harness,
  over,
  writing,
  asker,
  person,
  onAnswer,
  onStop,
}: {
  items: Item[];
  servers: McpServer[];
  harness: string;
  over: boolean;
  // Whether the agent may be writing the last item.
  writing: boolean;
  // Who is to answer the requests of the turn.
  asker: Who;
  person: (id?: string) => Who;
  onAnswer?: (request: string, option: string) => Promise<boolean>;
  onStop?: () => void;
}) {
  const approvals = items.filter((item) => item.type === "approval");
  const asked = (call?: string) =>
    approvals.find((item) => call && item.tool_call?.id === call);
  const calls = new Map(
    items.flatMap((item) =>
      item.type === "tool_call" ? [[item.id, item]] : [],
    ),
  );
  // A harness may give the tool call of a request only by its ID: the call
  // that it reported fills in the rest.
  const described = (request: ApprovalItem): ApprovalItem => {
    const call = calls.get(request.tool_call?.id);
    if (!call) return request;
    const { title, kind, input, mcp } = call;
    return {
      ...request,
      tool_call: { title, kind, input, mcp, ...request.tool_call },
    };
  };
  const out: ReactNode[] = [];
  let steps: ReactNode[] | null = null;
  const close = () => {
    if (steps)
      out.push(
        <div key={"steps" + out.length} className="steps">
          {steps}
        </div>,
      );
    steps = null;
  };
  items.forEach((item, index) => {
    if (item.type === "tool_call") {
      const approval = asked(item.id);
      // A tool call that waits for approval is shown by its request.
      if (approval && !approval.outcome) return;
      steps ??= [];
      steps.push(
        <Step
          key={item.id ?? index}
          item={item}
          approval={approval}
          server={serverOf(item, servers)}
          over={over}
          person={person}
        />,
      );
      return;
    }
    if (item.type === "approval") {
      if (!item.outcome) {
        close();
        const request = described(item);
        // The request stays the same one when the events of the turn come
        // after its record.
        out.push(
          <Request
            key={"request" + (item.id ?? index)}
            item={request}
            server={request.tool_call && serverOf(request.tool_call, servers)}
            asked={asker}
            onAnswer={onAnswer}
            onStop={onStop}
          />,
        );
      } else if (!calls.has(item.tool_call?.id)) {
        // The answer to a request shows with its tool call, if it has one.
        close();
        out.push(
          <Answered
            key={index}
            item={item}
            alone
            server={item.tool_call && serverOf(item.tool_call, servers)}
            person={person}
          />,
        );
      }
      return;
    }
    close();
    if (item.type === "message") {
      const typing = writing && index === items.length - 1;
      out.push(
        <div key={index} className={cls("bub-a", typing && "typing")}>
          <Markdown text={item.text} />
        </div>,
      );
    } else if (item.type === "thought") {
      out.push(
        <details key={index} className="th">
          <summary>
            <Icon name="think" />
            Thought
            <Icon name="chev" className="chev" />
          </summary>
          <p>{item.text}</p>
        </details>,
      );
    } else if (item.type === "plan") {
      out.push(<Plan key={index} entries={item.entries ?? []} over={over} />);
    } else if (item.type === "mcp_unsupported") {
      out.push(
        <Unsupported
          key={index}
          item={item}
          servers={servers}
          harness={harness}
        />,
      );
    }
  });
  close();
  return <>{out}</>;
}

// Footer says how the turn stands, below what the agent wrote.
function Footer({
  turn,
  stands,
  quiet,
  awaits,
  onStop,
  onAgain,
}: {
  turn: Turn;
  stands: Status;
  quiet: boolean;
  awaits?: "wake" | "restore";
  onStop?: () => void;
  onAgain?: (text: string) => void;
}) {
  if (stands === "queued") {
    return (
      <p className="state">
        <span className="c-dusk">
          <Icon name="clock" className="lg" />
        </span>
        <span>
          In the queue,{" "}
          <b>
            {turn.position === 1
              ? "next to run"
              : `${(turn.position ?? 0) - 1} ahead`}
          </b>
        </span>
        {onStop && (
          <button className="btn grey sm" type="button" onClick={onStop}>
            Cancel
          </button>
        )}
      </p>
    );
  }
  if (stands === "starting") {
    return (
      <p className="state">
        <span className="ring" />
        <span>
          {awaits === "restore"
            ? "Restoring files…"
            : awaits === "wake"
              ? "Waking up…"
              : "Starting…"}
        </span>
      </p>
    );
  }
  if (stands === "stopping") {
    return (
      <p className="state">
        <span className="ring" />
        <span>Stopping…</span>
      </p>
    );
  }
  if (stands === "running") {
    return quiet ? (
      <p className="state">
        <span className="ring" />
        <span>Working…</span>
      </p>
    ) : null;
  }
  if (stands === "waiting") return null;
  if (stands === "completed") {
    const usage = turn.usage;
    const stopped = turn.stop_reason && STOPS[turn.stop_reason];
    const more =
      usage &&
      (
        [
          ["thought_tokens", "thought"],
          ["cached_read_tokens", "read from cache"],
          ["cached_write_tokens", "written to cache"],
        ] as const
      )
        .filter(([field]) => usage[field])
        .map(([field, text]) => `${num(usage[field]!)} ${text}`)
        .join(", ");
    return (
      <p className="done-l">
        {stopped && (
          <>
            <span className="c-wait">{stopped}</span>
            {usage && <span className="sep">·</span>}
          </>
        )}
        {usage && (
          <span title={more || undefined}>
            {num(usage.input_tokens)} tokens in · {num(usage.output_tokens)} out
          </span>
        )}
      </p>
    );
  }
  const again = onAgain && (
    <div className="acts">
      <button
        className="btn sec sm"
        type="button"
        onClick={() => onAgain(turn.text ?? "")}
      >
        <Icon name="refresh" />
        Send again
      </button>
    </div>
  );
  if (stands === "failed") {
    return (
      <div className="fail">
        <Icon name="warn" className="lg" />
        <b>{FAILURES[turn.failure?.reason ?? ""] ?? "The turn failed"}</b>
        {turn.failure?.message && (
          <pre className="code wrapped">{turn.failure.message}</pre>
        )}
        {again}
      </div>
    );
  }
  if (stands === "expired") {
    return (
      <div className="fail soft">
        <Icon name="hour" className="lg" />
        <b>This turn waited too long in the queue</b>
        {again}
      </div>
    );
  }
  return (
    <div className="fail soft">
      <Icon name="cancel" className="lg" />
      <b>{turn.started_at ? "Stopped" : "Cancelled before it started"}</b>
    </div>
  );
}

// TurnView is one turn of a conversation: the message that was sent, and
// what the agent said and did, or has said and done so far.
export const TurnView = memo(function TurnView({
  turn,
  agent,
  servers = NONE,
  person,
  awaits,
  onStop,
  onAgain,
  onAnswer,
}: {
  turn: Turn;
  agent: Tinted<Agent>;
  // The MCP servers of the project, which name the servers of tool calls.
  servers?: McpServer[];
  // How to show the user with an ID.
  person: (id?: string) => Who;
  // What the turn waits for before it starts, if its session had no
  // sandbox that was awake: for the sandbox to wake, or for the files of
  // the session to be restored into a new one.
  awaits?: "wake" | "restore";
  // Stops the turn, for a caller who may while it has not ended.
  onStop?: () => void;
  // Takes the message of a turn that did not run to its end, to send it
  // again.
  onAgain?: (text: string) => void;
  // Answers a request of the turn for approval with one of its options,
  // for a caller who may. It reports whether the request has its answer.
  onAnswer?: (request: string, option: string) => Promise<boolean>;
}) {
  const stands = followed(turn);
  // The record of a turn that has not ended has no items, and lists the
  // requests that wait: they show before the events of the turn have come.
  const items = turn.items?.length
    ? turn.items
    : (turn.approvals ?? []).map((request): Item => ({
        type: "approval",
        ...request,
      }));
  const over = !!turn.ended_at;
  const headline =
    turn.ended_at && turn.started_at ? (
      <>
        <Time value={turn.created_at} /> ·{" "}
        {duration(Date.parse(turn.ended_at) - Date.parse(turn.started_at))}
      </>
    ) : turn.started_at && !over && stands !== "starting" ? (
      <>
        started <time title={turn.started_at}>{clock(turn.started_at)}</time>
      </>
    ) : (
      <Time value={turn.created_at} />
    );
  return (
    <div className="turn" id={"turn-" + turn.seq}>
      <div className="turn-h">
        <b>Turn {turn.seq}</b>
        <Chip tone={stands}>
          {stands === "queued"
            ? `Queued · ${turn.position}`
            : STATUS_NAMES[stands]}
        </Chip>
        {headline}
      </div>
      <div className="msg-u">
        <div className="bub-u">{turn.text}</div>
        <Avatar who={person(turn.created_by)} />
      </div>
      <div className="ag">
        <div className="ag-av">
          <AgentTile agent={agent} size="md" />
        </div>
        <div className="ag-c">
          <div className="ag-n">
            {label(agent)}
            {DOING[stands] && <small>{DOING[stands]}</small>}
          </div>
          <Blocks
            items={items}
            servers={servers}
            harness={agent.harness}
            over={over}
            writing={stands === "running"}
            asker={person(turn.created_by)}
            person={person}
            onAnswer={onAnswer}
            onStop={onStop}
          />
          <Footer
            turn={turn}
            stands={stands}
            quiet={!items.length}
            awaits={awaits}
            onStop={onStop}
            onAgain={onAgain}
          />
        </div>
      </div>
    </div>
  );
});
