import { useState } from "react";
import type { Item } from "../../api/types.ts";
import { cls } from "../../lib/cls.ts";
import { Icon } from "../../ui/Icon.tsx";
import type { Who } from "../../ui/who.ts";
import { CodeBlock } from "./Markdown.tsx";
import { changed, findDiff, inputOf, outsideOf, pathOf } from "./tools.ts";

type ApprovalItem = Extract<Item, { type: "approval" }>;
type Option = NonNullable<ApprovalItem["options"]>[number];

// Command is a command that a tool runs. Without a kind it is part of a
// step, which says where the command runs.
export function Command({
  input,
  kind,
  exit,
}: {
  input: Record<string, unknown>;
  kind?: string;
  // The code that the command exited with, if it is to be said.
  exit?: number | null;
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
      {exit != null && (
        <span className={cls("tag", exit !== 0 && "bad")}>exit {exit}</span>
      )}
    </div>
  );
}

// How the button of each kind of answer looks.
const STYLES: Record<string, string> = {
  allow_once: "pri",
  allow_always: "sec",
  reject_once: "dq",
  reject_always: "dq",
};

// An answer whose name is longer than this gets a line of its own.
const LONG = 26;

// Request is a request of an agent for approval that waits for its answer:
// what the agent is about to do, and the answers that it offers.
export function Request({
  item,
  server,
  asked,
  onAnswer,
  onStop,
}: {
  item: ApprovalItem;
  // The MCP server of the tool call, if it calls a tool of one.
  server?: string;
  // Who is to answer, for a caller who may not.
  asked: Who;
  // Answers the request with one of its options, for a caller who may. It
  // reports whether the request has its answer now.
  onAnswer?: (request: string, option: string) => Promise<boolean>;
  onStop?: () => void;
}) {
  // The name of the answer that is on its way.
  const [sending, setSending] = useState<string>();
  const call = item.tool_call ?? {};
  const input = inputOf(call.input);
  // A tool of an MCP server runs on the server, and what it is given says
  // nothing of the workspace.
  const command = !call.mcp && typeof input.command === "string";
  const diff = findDiff(input) ?? (call.mcp ? null : changed(input));
  const path = call.mcp ? "" : pathOf(input);
  const outside = outsideOf(input);
  const options = item.options ?? [];

  async function choose(option: Option) {
    setSending(option.name);
    // The request stays until the harness has the answer and says so.
    if (!(await onAnswer!(item.id ?? "", option.id))) setSending(undefined);
  }

  return (
    <div className="appr" role="group" aria-label="Request for approval">
      <div className="appr-h">
        <span className="appr-i">
          <Icon name="shield" className="lg" />
        </span>
        <div>
          <h3>
            {command
              ? outside.length
                ? "May I run this command outside the workspace?"
                : "May I run this command?"
              : call.kind === "edit"
                ? "May I change this file?"
                : "May I do this?"}
          </h3>
        </div>
      </div>
      {command ? (
        <>
          <Command input={input} kind={call.kind || "execute"} />
          {outside.length > 0 && (
            <p className="sub xs mono" style={{ margin: "-6px 2px 14px" }}>
              {outside.join(", ")}
            </p>
          )}
        </>
      ) : (
        <>
          <div className="cmd">
            <i>›</i>
            <span>{call.mcp?.tool ?? (path || call.title || call.kind)}</span>
            {call.mcp ? (
              <span className="tag srv">{server}</span>
            ) : (
              <span className="tag">{call.kind || "tool"}</span>
            )}
          </div>
          {diff ? (
            <CodeBlock text={diff} language="diff" bare={!!path} />
          ) : (
            Object.keys(input).length > 0 &&
            !path && (
              <pre className="code">{JSON.stringify(input, null, 2)}</pre>
            )
          )}
        </>
      )}
      {!onAnswer ? (
        <p className="appr-w">
          <Icon name="info" />
          Waiting for {asked.name} to answer
        </p>
      ) : sending ? (
        <p className="appr-w">
          <span className="ring" />
          <b>{sending}</b>
        </p>
      ) : (
        <div
          className={cls(
            "appr-a",
            options.some((option) => option.name.length > LONG) && "tall",
          )}
        >
          {options.map((option) => (
            <button
              key={option.id}
              className={"btn " + (STYLES[option.kind] ?? "sec")}
              type="button"
              onClick={() => void choose(option)}
            >
              {option.name}
            </button>
          ))}
          <span className="sp" />
          {onStop && (
            <button className="btn grey" type="button" onClick={onStop}>
              <Icon name="stop" />
              Stop turn
            </button>
          )}
        </div>
      )}
    </div>
  );
}
