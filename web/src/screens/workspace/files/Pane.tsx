import { useEffect, useState } from "react";
import { Group, Panel, Separator, usePanelRef } from "react-resizable-panels";
import { type ProjectContext, sessionAccess } from "../../../api/project.ts";
import type { Me, Session } from "../../../api/types.ts";
import { cls } from "../../../lib/cls.ts";
import { prefs } from "../../../lib/prefs.ts";
import { Empty } from "../../../ui/Blank.tsx";
import { Icon } from "../../../ui/Icon.tsx";
import { useShown } from "./state.ts";
import { Tree } from "./Tree.tsx";
import { View } from "./View.tsx";

// How wide the tree is beside the open files until the caller gives it
// another width, in pixels.
const TREE = 232;

// FilesPane is the pane of the files of a session: the tree of its
// workspace, and beside it the files that are open, if any are. pulse
// changes when the agent of the session has done something.
export function FilesPane({
  context,
  me,
  session,
  pulse,
  onClose,
}: {
  context: ProjectContext;
  me: Me;
  session: Session;
  pulse: string;
  onClose: () => void;
}) {
  const shown = useShown(session.id);
  // The tree shows beside the open files unless the caller has hidden it,
  // here or before.
  const [tree, setTree] = useState(() => prefs.get("files.tree") !== "0");
  const [wide, setWide] = useState(false);
  const viewing = shown.tabs.length > 0;
  const filling = wide && viewing;
  useEffect(() => {
    if (!filling) return;
    const key = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !event.defaultPrevented) setWide(false);
    };
    document.addEventListener("keydown", key);
    return () => document.removeEventListener("keydown", key);
  }, [filling]);
  const beside = usePanelRef();
  // The tree takes its width again once the first file has opened: it may
  // have been pressed narrower while the pane made room for the file.
  useEffect(() => {
    if (!viewing || !tree) return;
    const later = requestAnimationFrame(() =>
      beside.current?.resize(Number(prefs.get("files.width")) || TREE),
    );
    return () => cancelAnimationFrame(later);
  }, [viewing, tree, beside]);

  if (session.ended_at) {
    return (
      <aside className="pane files">
        <div className="r-top">
          <h2 className="h3">Files</h2>
          <button
            className="ib sm"
            type="button"
            title="Close"
            onClick={onClose}
          >
            <Icon name="chev" />
          </button>
        </div>
        <Empty glyph="trash" title="Deleted with the session" />
      </aside>
    );
  }

  const { send } = sessionAccess(context, me, session);
  return (
    <aside className={cls("pane files", filling && "wide")}>
      <Group
        className="fp"
        onLayoutChanged={(_, how) => {
          const width = beside.current?.getSize().inPixels;
          if (how.isUserInteraction && viewing && width) {
            prefs.set("files.width", String(Math.round(width)));
          }
        }}
      >
        {viewing && (
          <Panel id="view" minSize={260}>
            <View
              session={session.id}
              shown={shown}
              tree={tree}
              wide={filling}
              onTree={() => {
                prefs.set("files.tree", tree ? "0" : "1");
                setTree(!tree);
              }}
              onWide={() => setWide(!wide)}
            />
          </Panel>
        )}
        {viewing && tree && <Separator className="fp-sep" />}
        {(tree || !viewing) && (
          // Beside the open files the tree is another panel than alone: one
          // that keeps its width, in pixels, as the pane gets wider.
          <Panel
            id={viewing ? "tree" : "alone"}
            panelRef={beside}
            defaultSize={
              viewing ? Number(prefs.get("files.width")) || TREE : undefined
            }
            minSize={viewing ? 200 : undefined}
            maxSize={viewing ? "60%" : undefined}
            groupResizeBehavior={viewing ? "preserve-pixel-size" : undefined}
          >
            <Tree
              // The tree of another session begins anew.
              key={session.id}
              session={session}
              shown={shown}
              writes={send}
              pulse={pulse}
              onClose={onClose}
            />
          </Panel>
        )}
      </Group>
    </aside>
  );
}
