import { useState } from "react";
import { saveAttachment } from "../../api/conversation.ts";
import type { Attachment } from "../../api/types.ts";
import { size } from "../../lib/format.ts";
import { Icon } from "../../ui/Icon.tsx";
import { oops } from "../../ui/toast.ts";

// Carried shows the files that a message carries, in its order. A file is
// saved with a click while the API has it, which is until its session ends.
export function Carried({
  files,
  kept,
}: {
  files: Attachment[];
  kept: boolean;
}) {
  return (
    <ul className="files" aria-label="Files">
      {files.map((file) => (
        <li key={file.id}>
          <Saved file={file} kept={kept} />
        </li>
      ))}
    </ul>
  );
}

function Saved({ file, kept }: { file: Attachment; kept: boolean }) {
  const [saving, setSaving] = useState(false);
  const shown = (
    <>
      {saving ? <span className="ring" /> : <Icon name="doc" />}
      <b>{file.name}</b>
      <small>{size(file.size)}</small>
    </>
  );
  if (!kept) {
    return (
      <span className="file" title={file.name}>
        {shown}
      </span>
    );
  }
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
      className="file"
      type="button"
      title={"Download " + file.name}
      disabled={saving}
      onClick={() => void save()}
    >
      {shown}
    </button>
  );
}
