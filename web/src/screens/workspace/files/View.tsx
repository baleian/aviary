import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { lazy, Suspense, useCallback, useState } from "react";
import { saveFile } from "../../../api/conversation.ts";
import type { Entry } from "../../../api/types.ts";
import { readFile } from "../../../api/workspace.ts";
import { cls } from "../../../lib/cls.ts";
import { sentence, size } from "../../../lib/format.ts";
import { copy } from "../../../ui/clipboard.ts";
import { Icon } from "../../../ui/Icon.tsx";
import { Segmented } from "../../../ui/Segmented.tsx";
import { Time } from "../../../ui/Time.tsx";
import { oops } from "../../../ui/toast.ts";
import { Document, Picture } from "../Viewer.tsx";
import { Doc } from "./Doc.tsx";
import { isText, type Sort, sortOf } from "./kinds.ts";
import {
  closeFiles,
  nameOf,
  openFile,
  parentOf,
  reveal,
  setSource,
  type Shown,
  useEntry,
} from "./state.ts";

// The code is read, with what colours it, when the first file needs it.
const Code = lazy(() => import("./Code.tsx"));

// How much of a text is read and shown, in bytes.
const FIRST = 1 << 20;

// The largest file that is read whole to be shown, in bytes: a picture, or
// a PDF.
const LARGEST = 32 << 20;

// What was read of a file: its bytes, and its text if it is one.
interface Content {
  bytes: Blob;
  text?: string;
}

// View is the files that are open, each in a tab, and the one in view: as
// what it makes if the web UI can show that, or else as it is written.
export function View({
  session,
  shown,
  tree,
  wide,
  onTree,
  onWide,
}: {
  session: string;
  shown: Shown;
  // Whether the tree shows beside the files, and whether the files take the
  // whole window.
  tree: boolean;
  wide: boolean;
  onTree: () => void;
  onWide: () => void;
}) {
  const at = shown.at ?? shown.tabs[0];
  const names = shown.tabs.map(nameOf);
  return (
    <div className="fv">
      <div className="fv-tabs">
        <div className="tabs" role="tablist" aria-label="Open files">
          {shown.tabs.map((path, index) => {
            const { glyph, kind } = sortOf(names[index]);
            // Files of the same name are told apart by where they are.
            const twin =
              names.indexOf(names[index]) !== names.lastIndexOf(names[index]);
            return (
              <span
                key={path}
                className={cls("tab", path === at && "on")}
                role="tab"
                aria-selected={path === at}
                tabIndex={0}
                title={path}
                onClick={() => openFile(session, path)}
                onKeyDown={(event) => {
                  if (event.key === "Enter" || event.key === " ") {
                    event.preventDefault();
                    openFile(session, path);
                  }
                }}
                onAuxClick={(event) => {
                  if (event.button === 1) closeFiles(session, [path]);
                }}
                ref={(node) => {
                  if (path === at)
                    node?.scrollIntoView?.({
                      block: "nearest",
                      inline: "nearest",
                    });
                }}
              >
                <Icon name={glyph} className={"mk " + kind.family} />
                <span className="nm">{names[index]}</span>
                {twin && <small>{nameOf(parentOf(path)) || "workspace"}</small>}
                <button
                  className="ib xs"
                  type="button"
                  title="Close"
                  aria-label={"Close " + names[index]}
                  onClick={(event) => {
                    event.stopPropagation();
                    closeFiles(session, [path]);
                  }}
                >
                  <Icon name="x" />
                </button>
              </span>
            );
          })}
        </div>
        <span className="ft-acts">
          {shown.tabs.length > 1 && (
            <button
              className="ib sm"
              type="button"
              title="Close all"
              onClick={() => closeFiles(session, shown.tabs)}
            >
              <Icon name="cancel" />
            </button>
          )}
          <button
            className={cls("ib sm", wide && "tog")}
            type="button"
            title={wide ? "Back to the conversation" : "Fill the window"}
            aria-pressed={wide}
            onClick={onWide}
          >
            <Icon name="wide" />
          </button>
          <button
            className={cls("ib sm", tree && "tog")}
            type="button"
            title={tree ? "Hide the tree" : "Show the tree"}
            aria-pressed={tree}
            onClick={onTree}
          >
            <Icon name="panel" />
          </button>
        </span>
      </div>
      {at && (
        <Opened
          // What was scrolled of one file is not that of another.
          key={at}
          session={session}
          path={at}
          written={shown.source.has(at)}
        />
      )}
    </div>
  );
}

// Opened is a file in view: where it is, what there is to know of it, what
// can be done with it, and then the file itself.
function Opened({
  session,
  path,
  written,
}: {
  session: string;
  path: string;
  // Whether the file shows as it is written.
  written: boolean;
}) {
  const name = nameOf(path);
  const sort = sortOf(name);
  const entry = useEntry(session, path);
  // A picture and a PDF are read whole, and anything else as text.
  const whole = (sort.look === "image" || sort.look === "pdf") && !written;
  const large = whole && (entry?.size ?? 0) > LARGEST;
  const { data, error, isPending, refetch } = useQuery({
    queryKey: ["file", session, path, whole, entry?.size, entry?.modified_at],
    queryFn: async (): Promise<Content> => {
      if (whole) return { bytes: await readFile(session, path) };
      const bytes = await readFile(session, path, FIRST);
      // A file whose name does not tell is text if it reads as one.
      if (!sort.look && !(await isText(bytes))) return { bytes };
      const text = await bytes.text();
      // What was cut may end within a character.
      return { bytes, text: text.replace(/�$/, "") };
    },
    enabled: !large,
    // A file that changed shows as it was until it is read again.
    placeholderData: keepPreviousData,
    staleTime: Infinity,
    gcTime: 60000,
    refetchOnWindowFocus: false,
    retry: false,
  });
  // A file that a document links to opens, and the tree shows where it is.
  const follow = useCallback(
    (file: string) => {
      reveal(session, file);
      openFile(session, file);
    },
    [session],
  );
  const [wrap, setWrap] = useState(false);
  const [saving, setSaving] = useState(false);
  const [fact, setFact] = useState<string>();
  const [broken, setBroken] = useState(false);

  async function save() {
    setSaving(true);
    try {
      saveFile(name, await readFile(session, path));
    } catch (failure) {
      oops(failure);
    }
    setSaving(false);
  }

  const code = data?.text !== undefined && (written || shows(sort) === "code");
  const cut = code && (entry?.size ?? 0) > data.bytes.size;
  const crumbs = path.split("/");
  return (
    <>
      <div className="fv-bar">
        <nav
          className="crumbs"
          aria-label="Path"
          title={"/home/workspace/" + path}
        >
          {/* A path that does not fit loses its beginning. */}
          <span className="in">
            <button type="button" onClick={() => reveal(session, "")}>
              workspace
            </button>
            {crumbs.map((crumb, index) => {
              const to = crumbs.slice(0, index + 1).join("/");
              return (
                <span key={to}>
                  <Icon name="chev" />
                  {index < crumbs.length - 1 ? (
                    <button
                      type="button"
                      onClick={() => reveal(session, to, true)}
                    >
                      {crumb}
                    </button>
                  ) : (
                    <b>{crumb}</b>
                  )}
                </span>
              );
            })}
          </span>
        </nav>
        <span className="meta">
          {entry?.size !== undefined && size(entry.size)}
          {fact && " · " + fact}
          {entry && (
            <>
              {" · "}
              <Time value={entry.modified_at} />
            </>
          )}
        </span>
        {sort.written && (
          <Segmented
            choices={[
              ["shown", "Preview", "eye"],
              ["written", "Source", "code"],
            ]}
            value={written ? "written" : "shown"}
            onChange={(value) => setSource(session, path, value === "written")}
          />
        )}
        <span className="ft-acts">
          {code && (
            <button
              className={cls("ib sm", wrap && "tog")}
              type="button"
              title="Wrap long lines"
              aria-pressed={wrap}
              onClick={() => setWrap(!wrap)}
            >
              <Icon name="wrap" />
            </button>
          )}
          <button
            className="ib sm"
            type="button"
            title="Copy the path"
            onClick={() => void copy("/home/workspace/" + path, "path")}
          >
            <Icon name="copy" />
          </button>
          <button
            className="ib sm"
            type="button"
            title="Download"
            disabled={saving}
            onClick={() => void save()}
          >
            {saving ? <span className="ring" /> : <Icon name="download" />}
          </button>
        </span>
      </div>
      <div className={cls("fv-b", !code && "mid")}>
        {large ? (
          <Without
            says="Too large to show here"
            saving={saving}
            onSave={() => void save()}
          />
        ) : isPending ? (
          <span className="ring" />
        ) : error || broken ? (
          <p className="fv-no">
            <Icon name="warn" className="lg" />
            {error ? sentence(error.message) : "This file cannot be shown."}
            {error && (
              <button
                className="btn sec sm"
                type="button"
                onClick={() => void refetch()}
              >
                Try again
              </button>
            )}
          </p>
        ) : code ? (
          <Suspense fallback={<span className="ring" />}>
            <Code text={data.text!} name={name} wrap={wrap} />
          </Suspense>
        ) : data.text !== undefined && sort.look === "markdown" ? (
          <div className="fv-scroll">
            <Doc
              session={session}
              path={path}
              text={data.text}
              onOpen={follow}
            />
          </div>
        ) : data.text !== undefined && sort.look === "page" ? (
          <iframe
            className="fv-page"
            title={name}
            // The page runs its scripts apart from the web UI: it has no
            // origin, and so nothing of the caller's.
            sandbox="allow-scripts"
            srcDoc={data.text}
          />
        ) : sort.look === "image" ? (
          <div className="fv-pic">
            <Picture
              file={{ name, kind: sort.kind }}
              data={data.bytes}
              onFact={setFact}
              onBroken={() => setBroken(true)}
            />
          </div>
        ) : sort.look === "pdf" ? (
          <div className="fv-scroll dark">
            <Document
              data={data.bytes}
              onFact={setFact}
              onBroken={() => setBroken(true)}
            />
          </div>
        ) : (
          <Without
            says="No preview"
            saving={saving}
            onSave={() => void save()}
          />
        )}
      </div>
      {cut && (
        <p className="fv-cut">
          The first {size(data.bytes.size)} of {size((entry as Entry).size!)}.
        </p>
      )}
    </>
  );
}

// shows tells how a file shows when it shows as what it makes.
function shows(sort: Sort): Sort["look"] {
  return sort.look ?? "code";
}

// Without stands where a file cannot be shown, and saves it instead.
function Without({
  says,
  saving,
  onSave,
}: {
  says: string;
  saving: boolean;
  onSave: () => void;
}) {
  return (
    <div className="fv-none">
      <Icon name="file" className="xl" />
      <b>{says}</b>
      <button
        className="btn sec sm"
        type="button"
        disabled={saving}
        onClick={onSave}
      >
        {saving ? <span className="ring" /> : <Icon name="download" />}
        Download
      </button>
    </div>
  );
}
