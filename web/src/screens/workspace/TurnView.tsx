import type { ReactNode } from "react";
import type { Tinted } from "../../api/queries.ts";
import type { Agent, Item, PlanEntry, Turn } from "../../api/types.ts";
import { cls } from "../../lib/cls.ts";
import { clock, duration, label, num } from "../../lib/format.ts";
import { type Status, status, STATUS_NAMES } from "../../lib/sessions.ts";
import { AgentTile, Avatar } from "../../ui/Avatar.tsx";
import { Chip } from "../../ui/Chip.tsx";
import { Icon } from "../../ui/Icon.tsx";
import type { IconName } from "../../ui/icons.ts";
import { Time } from "../../ui/Time.tsx";
import type { Who } from "../../ui/who.ts";
import { CodeBlock, Markdown } from "./Markdown.tsx";
import {
  changed,
  exitCode,
  findDiff,
  inputOf,
  outputText,
  pathOf,
} from "./tools.ts";

type ToolItem = Extract<Item, { type: "tool_call" }>;
type ApprovalItem = Extract<Item, { type: "approval" }>;

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

function Command({
  input,
  kind,
}: {
  input: Record<string, unknown>;
  kind?: string;
}) {
  return (
    <div className={cls("cmd", !kind && "in")}>
      <i>$</i>
      <span>
        {String(input.command)}
        {!kind && typeof input.cwd === "string" && (
          <small>in {input.cwd}</small>
        )}
      </span>
      {kind && <span className="tag">{kind}</span>}
    </div>
  );
}

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
  person,
}: {
  item: ApprovalItem;
  alone?: boolean;
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
        {alone && item.tool_call?.title && (
          <>
            <b>{item.tool_call.title}</b>:{" "}
          </>
        )}
        <b>{option.name}</b>
        {item.answered_by && <> by {person(item.answered_by).name}</>}
      </span>
    </p>
  );
}

// Step is a tool call: what was run or changed, and what came of it.
function Step({
  item,
  approval,
  over,
  person,
}: {
  item: ToolItem;
  approval?: ApprovalItem;
  over: boolean;
  person: (id?: string) => Who;
}) {
  const input = inputOf(item.input);
  const kind = item.kind || "other";
  const diff = findDiff(item.output) ?? findDiff(item.input) ?? changed(input);
  const output = outputText(item.output);
  const code = exitCode(item.output);
  const failed =
    item.status === "failed" &&
    !output &&
    typeof (item.output as { error?: unknown } | undefined)?.error !== "string";
  const raw = Object.fromEntries(
    (["input", "output", "meta"] as const)
      .filter((field) => item[field] !== undefined)
      .map((field) => [field, item[field]]),
  );
  const title = item.title || kind;
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
          <Icon name={approval ? "shield" : (KINDS[kind] ?? "cube")} />
        </span>
        <span
          className={cls(
            "step-t",
            kind === "execute" && title === input.command && "cmdline",
          )}
        >
          {title}
        </span>
        <span className="tag">{kind}</span>
        {mark}
        <Icon name="chev" className="chev" />
      </summary>
      <div className="step-b">
        {approval && <Answered item={approval} person={person} />}
        {typeof input.command === "string" ? (
          <Command input={input} />
        ) : (
          pathOf(input) && <p className="sub xs mono">{pathOf(input)}</p>
        )}
        {diff ? (
          <CodeBlock text={diff} language="diff" />
        ) : (
          output && <pre className="code">{output}</pre>
        )}
        {code != null && (code !== 0 || !output) && (
          <p className="sub xs">
            Exited with code <b>{code}</b>.
          </p>
        )}
        {failed && <p className="sub xs">The tool call failed.</p>}
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
}

// Blocks is what the agent said and did, in order. Tool calls that follow
// each other are one list of steps.
function Blocks({
  items,
  over,
  person,
}: {
  items: Item[];
  over: boolean;
  person: (id?: string) => Who;
}) {
  const approvals = items.filter((item) => item.type === "approval");
  const asked = (call?: string) =>
    approvals.find((item) => call && item.tool_call?.id === call);
  const tools = new Set(
    items.filter((item) => item.type === "tool_call").map((item) => item.id),
  );
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
          over={over}
          person={person}
        />,
      );
      return;
    }
    if (item.type === "approval") {
      // The answer to a request shows with its tool call, if it has one.
      if (item.outcome && !tools.has(item.tool_call?.id)) {
        close();
        out.push(<Answered key={index} item={item} alone person={person} />);
      }
      return;
    }
    close();
    if (item.type === "message") {
      out.push(
        <div key={index} className="bub-a">
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
}: {
  turn: Turn;
  stands: Status;
  quiet: boolean;
}) {
  if (stands === "queued") {
    return (
      <p className="state">
        <span className="c-dusk">
          <Icon name="clock" className="lg" />
        </span>
        <span>In the queue</span>
      </p>
    );
  }
  if (stands === "starting" || stands === "stopping") {
    return (
      <p className="state">
        <span className="ring" />
        <span>{stands === "starting" ? "Starting…" : "Stopping…"}</span>
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
  if (stands === "failed") {
    return (
      <div className="fail">
        <Icon name="warn" className="lg" />
        <b>{FAILURES[turn.failure?.reason ?? ""] ?? "The turn failed"}</b>
        {turn.failure?.message && (
          <pre className="code wrapped">{turn.failure.message}</pre>
        )}
      </div>
    );
  }
  if (stands === "expired") {
    return (
      <div className="fail soft">
        <Icon name="hour" className="lg" />
        <b>This turn waited too long in the queue</b>
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
// what the agent said and did.
export function TurnView({
  turn,
  items,
  agent,
  person,
}: {
  turn: Turn;
  // What the agent said and did so far.
  items: Item[];
  agent: Tinted<Agent>;
  // How to show the user with an ID.
  person: (id?: string) => Who;
}) {
  const stands = status(turn);
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
        <Chip tone={stands}>{STATUS_NAMES[stands]}</Chip>
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
          <Blocks items={items} over={over} person={person} />
          <Footer turn={turn} stands={stands} quiet={!items.length} />
        </div>
      </div>
    </div>
  );
}
