import { type ReactNode, useSyncExternalStore } from "react";
import { Group, Panel, Separator } from "react-resizable-panels";
import { cls } from "../../lib/cls.ts";
import { prefs } from "../../lib/prefs.ts";
import { Icon } from "../../ui/Icon.tsx";

// What the pane beside a conversation shows: the details of the session,
// the tree of its files, or the tree with the files that are open.
export type Beside = "details" | "tree" | "files";

// Which pane the caller has beside the conversations: one of them, or none.
export type Pane = "files" | "details" | null;

// How wide each pane is until the caller gives it another width, and how
// narrow and how wide it may get: in pixels, or as a share of what the
// conversation and the pane have together.
const WIDTHS: Record<
  Beside,
  { first: number | string; least: number; most: number | string }
> = {
  details: { first: 316, least: 264, most: 460 },
  tree: { first: 316, least: 240, most: 560 },
  files: { first: "62%", least: 440, most: "82%" },
};

// The narrowest that a conversation gets, in pixels.
const LEAST = 340;

// Below this width of the window there is no room for a pane beside the
// conversation.
const NARROW = "(max-width: 1240px)";

function useNarrow(): boolean {
  return useSyncExternalStore(
    (listener) => {
      const media = window.matchMedia?.(NARROW);
      media?.addEventListener("change", listener);
      return () => media?.removeEventListener("change", listener);
    },
    () => window.matchMedia?.(NARROW).matches ?? false,
  );
}

// widthOf returns the width that the caller gave a pane, here or before, or
// else the one that it begins with.
const widthOf = (beside: Beside) =>
  prefs.get("pane." + beside) ?? WIDTHS[beside].first;

// Split is a conversation with a pane beside it, if one shows. The line
// between them is dragged, or moved with the arrow keys, to give each its
// share of the window, which is kept for each kind of pane. In a narrow
// window the files lie over the conversation, and the details do not show.
export function Split({
  beside,
  side,
  children,
}: {
  beside?: Beside;
  side?: ReactNode;
  children: ReactNode;
}) {
  const narrow = useNarrow();
  const shown = !!side && !!beside && !narrow;
  return (
    <div className="work">
      <Group
        onLayoutChanged={(layout, how) => {
          if (how.isUserInteraction && beside && layout[beside] !== undefined) {
            prefs.set("pane." + beside, layout[beside] + "%");
          }
        }}
      >
        <Panel id="main" minSize={LEAST}>
          {children}
        </Panel>
        {shown && (
          <Separator className="grip">
            <i />
          </Separator>
        )}
        {shown && (
          // The pane is named after what it shows, so that each kind keeps
          // its own width, and what it shows stays as the kind changes.
          <Panel
            id={beside}
            defaultSize={widthOf(beside)}
            minSize={WIDTHS[beside].least}
            maxSize={WIDTHS[beside].most}
          >
            {side}
          </Panel>
        )}
      </Group>
      {narrow && side && beside !== "details" && (
        <div className="over-pane">{side}</div>
      )}
    </div>
  );
}

// PaneButtons show and hide the panes beside a conversation: the files of
// the session, if it has any to show, and its details.
export function PaneButtons({
  pane,
  files,
  onPane,
}: {
  pane: Pane;
  // Whether there is a session whose files can show.
  files: boolean;
  onPane: (pane: Pane) => void;
}) {
  return (
    <>
      {files && (
        <button
          className={cls("btn sec sm", pane === "files" && "tog")}
          type="button"
          aria-pressed={pane === "files"}
          onClick={() => onPane(pane === "files" ? null : "files")}
        >
          <Icon name="folder" />
          <span>Files</span>
        </button>
      )}
      <button
        className={cls("btn sec sm dtl", pane === "details" && "tog")}
        type="button"
        aria-pressed={pane === "details"}
        onClick={() => onPane(pane === "details" ? null : "details")}
      >
        <Icon name="panel" />
        <span>Details</span>
      </button>
    </>
  );
}
