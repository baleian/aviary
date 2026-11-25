import { type ReactNode, useMemo, useRef, useState } from "react";
import { saveAttachment } from "../../api/conversation.ts";
import type { Attachment } from "../../api/types.ts";
import { cls } from "../../lib/cls.ts";
import { split } from "../../lib/files.ts";
import { size } from "../../lib/format.ts";
import { Icon } from "../../ui/Icon.tsx";
import { oops } from "../../ui/toast.ts";
import { carriedFile, type Shown, useSeen, useThumbnail } from "./shown.ts";
import { Viewer } from "./Viewer.tsx";

// FileCard is the box of a file: a tile that says what the file is, with a
// picture of it if there is one, then its name and its size, and at the end
// what can be done with it. The box opens the file if onOpen is given.
export function FileCard({
  file,
  note,
  gone,
  reads = true,
  onOpen,
  children,
}: {
  file: Shown;
  // What stands below the name instead of the size of the file.
  note?: ReactNode;
  // How much of the file has gone while it is uploaded, in percent.
  gone?: number;
  // Whether the file may be read for its thumbnail.
  reads?: boolean;
  onOpen?: () => void;
  children?: ReactNode;
}) {
  const box = useRef<HTMLDivElement>(null);
  const seen = useSeen(box);
  const picture = useThumbnail(file, reads && seen);
  const { ext, family } = file.kind;
  // A name that does not fit is cut before its end, which tells it apart.
  const [head, tail] = split(file.name);
  const face = (
    <>
      <span className={cls("tile", family)}>
        {picture ? (
          <img src={picture} alt="" />
        ) : ext && ext.length <= 4 ? (
          ext
        ) : (
          <Icon name="doc" />
        )}
      </span>
      <span className="what">
        <b title={file.name}>
          <span>{head}</span>
          {tail && <span>{tail}</span>}
        </b>
        <small>{note ?? size(file.size)}</small>
      </span>
    </>
  );
  return (
    <div className="file" ref={box}>
      {onOpen ? (
        <button
          className="face"
          type="button"
          aria-label={"Open " + file.name}
          onClick={onOpen}
        >
          {face}
        </button>
      ) : (
        <span className="face">{face}</span>
      )}
      {children}
      {gone !== undefined && (
        <i className="gone" style={{ width: gone + "%" }} />
      )}
    </div>
  );
}

// Carried shows the files that a message carries, in its order. While the
// API has them, which is until their session ends, a file is saved with the
// button of its box.
export function Carried({
  files,
  kept,
}: {
  files: Attachment[];
  kept: boolean;
}) {
  const shown = useMemo(() => files.map(carriedFile), [files]);
  // Those that the web UI can show, and which of them it shows.
  const viewed = useMemo(() => shown.filter((file) => file.kind.view), [shown]);
  const [open, setOpen] = useState<number>();
  return (
    <>
      <ul className="files" aria-label="Files">
        {shown.map((file, at) => (
          <li key={file.key}>
            <FileCard
              file={file}
              reads={kept}
              onOpen={
                kept && file.kind.view
                  ? () => setOpen(viewed.indexOf(file))
                  : undefined
              }
            >
              {kept && <Save file={files[at]} />}
            </FileCard>
          </li>
        ))}
      </ul>
      {open !== undefined && viewed[open] && (
        <Viewer
          files={viewed}
          at={open}
          saves
          onMove={setOpen}
          onClose={() => setOpen(undefined)}
        />
      )}
    </>
  );
}

function Save({ file }: { file: Attachment }) {
  const [saving, setSaving] = useState(false);
  async function save() {
    setSaving(true);
    try {
      await saveAttachment(file);
    } catch (error) {
      oops(error);
    }
    setSaving(false);
  }
  return (
    <button
      className="ib sm"
      type="button"
      title="Download"
      aria-label={"Download " + file.name}
      disabled={saving}
      onClick={() => void save()}
    >
      {saving ? <span className="ring" /> : <Icon name="download" />}
    </button>
  );
}
