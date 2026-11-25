import { useQuery } from "@tanstack/react-query";
import {
  useEffect,
  useEffectEvent,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { saveFile } from "../../api/conversation.ts";
import { cls } from "../../lib/cls.ts";
import { plural, sentence, size } from "../../lib/format.ts";
import type { Pages } from "../../lib/pdf.ts";
import { Icon } from "../../ui/Icon.tsx";
import { oops } from "../../ui/toast.ts";
import { type Shown, useNear } from "./shown.ts";

// How much of a text is read and shown, in bytes.
const FIRST = 1 << 20;

// Viewer shows a file over the page: a picture, the pages of a PDF, or a
// text. files are the files that it goes through with its arrows, and at is
// the one that it shows. A file that the API has can be saved from it.
export function Viewer({
  files,
  at,
  saves = false,
  onMove,
  onClose,
}: {
  files: Shown[];
  at: number;
  saves?: boolean;
  onMove: (at: number) => void;
  onClose: () => void;
}) {
  const file = files[at];
  const dialog = useRef<HTMLDialogElement>(null);
  useLayoutEffect(() => {
    const node = dialog.current;
    if (!node || node.open) return;
    node.showModal();
    // The keys go to the viewer itself, and not to one of its buttons.
    node.focus();
    // Closing it gives the focus back to where it was, which the browser
    // does only for a dialog that is still on the page.
    return () => node.close();
  }, []);

  const text = file.kind.view === "text";
  const { data, error, isPending } = useQuery({
    queryKey: ["file", file.key, text],
    queryFn: () => file.read(text ? FIRST : undefined),
    staleTime: Infinity,
    gcTime: 60000,
    retry: false,
  });
  // What the file turned out to be, such as how large a picture is.
  const [fact, setFact] = useState<{ of: string; says: string }>();
  const tell = (says: string) => setFact({ of: file.key, says });
  // A file that is shown but cannot be drawn.
  const [broken, setBroken] = useState<string>();

  const many = files.length > 1;
  const move = (by: number) => onMove((at + by + files.length) % files.length);
  const [saving, setSaving] = useState(false);
  async function save() {
    setSaving(true);
    try {
      // What was read of a text may not be all of it.
      saveFile(file.name, data && !text ? data : await file.read());
    } catch (failure) {
      oops(failure);
    }
    setSaving(false);
  }

  return (
    <dialog
      ref={dialog}
      className="viewer"
      data-theme="dark"
      tabIndex={-1}
      aria-label={file.name}
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
      onKeyDown={(event) => {
        if (!many) return;
        if (event.key === "ArrowLeft") move(-1);
        if (event.key === "ArrowRight") move(1);
      }}
    >
      <header className="vw-h">
        <div className="vw-t">
          <b>{file.name}</b>
          <small>
            {size(file.size)}
            {fact?.of === file.key && " · " + fact.says}
          </small>
        </div>
        {many && (
          <span className="vw-n">
            {at + 1} / {files.length}
          </span>
        )}
        {saves && (
          <button
            className="ib"
            type="button"
            title="Download"
            disabled={saving}
            onClick={() => void save()}
          >
            {saving ? (
              <span className="ring" />
            ) : (
              <Icon name="download" className="lg" />
            )}
          </button>
        )}
        <button className="ib" type="button" title="Close" onClick={onClose}>
          <Icon name="x" className="lg" />
        </button>
      </header>
      <div
        // What is shown of another file begins at its top.
        key={file.key}
        className="vw-b"
        onPointerDown={({ target, currentTarget: body, nativeEvent: at }) => {
          // A press beside the file is one on what is behind it, unless it
          // is on a scrollbar, which lies outside of what is scrolled.
          const beside = at.offsetX < body.clientWidth;
          if (target === body && beside && at.offsetY < body.clientHeight) {
            onClose();
          }
        }}
      >
        {isPending ? (
          <span className="ring" />
        ) : error || broken === file.key ? (
          <p className="vw-no">
            <Icon name="warn" className="lg" />
            {error ? sentence(error.message) : "This file cannot be shown."}
          </p>
        ) : file.kind.view === "image" ? (
          <Picture
            key={file.key}
            file={file}
            data={data}
            onFact={tell}
            onBroken={() => setBroken(file.key)}
          />
        ) : file.kind.view === "pdf" ? (
          <Document
            key={file.key}
            data={data}
            onFact={tell}
            onBroken={() => setBroken(file.key)}
          />
        ) : (
          <Text key={file.key} file={file} data={data} />
        )}
      </div>
      {many && (
        <>
          <button
            className="vw-go"
            type="button"
            title="Previous"
            onClick={() => move(-1)}
          >
            <Icon name="chev" className="lg" />
          </button>
          <button
            className="vw-go next"
            type="button"
            title="Next"
            onClick={() => move(1)}
          >
            <Icon name="chev" className="lg" />
          </button>
        </>
      )}
    </dialog>
  );
}

// Picture shows a picture as large as fits, and at its own size after a
// click on it, if that is larger.
function Picture({
  file,
  data,
  onFact,
  onBroken,
}: {
  file: Shown;
  data: Blob;
  onFact: (says: string) => void;
  onBroken: () => void;
}) {
  const picture = useRef<HTMLImageElement>(null);
  // The picture is given the address of the file before it is first shown,
  // and the address is let go of with it.
  useLayoutEffect(() => {
    // The file is drawn as what its name says it is, whatever it came as.
    const made = URL.createObjectURL(data.slice(0, data.size, file.kind.type));
    picture.current!.src = made;
    return () => URL.revokeObjectURL(made);
  }, [data, file.kind.type]);
  // Whether the picture is smaller than itself as it is shown, and whether
  // it is shown at its own size.
  const [shrunk, setShrunk] = useState(false);
  const [whole, setWhole] = useState(false);
  return (
    <img
      ref={picture}
      className={cls(whole ? "whole" : shrunk && "shrunk")}
      alt={file.name}
      onLoad={({ currentTarget: loaded }) => {
        onFact(`${loaded.naturalWidth} × ${loaded.naturalHeight}`);
        setShrunk(loaded.naturalWidth > loaded.clientWidth + 1);
      }}
      onError={onBroken}
      onClick={() => (shrunk || whole) && setWhole(!whole)}
    />
  );
}

// Document shows the pages of a PDF below one another, each drawn when it
// comes near.
function Document({
  data,
  onFact,
  onBroken,
}: {
  data: Blob;
  onFact: (says: string) => void;
  onBroken: () => void;
}) {
  const [pages, setPages] = useState<Pages>();
  const tell = useEffectEvent(onFact);
  const broke = useEffectEvent(onBroken);
  useEffect(() => {
    let opened: Pages | undefined;
    let gone = false;
    import("../../lib/pdf.ts")
      .then(({ open }) => open(data))
      .then((read) => {
        if (gone) return read.close();
        opened = read;
        setPages(read);
        tell(plural(read.count, "page"));
      })
      .catch(() => gone || broke());
    return () => {
      gone = true;
      opened?.close();
    };
  }, [data]);
  if (!pages) return <span className="ring" />;
  return (
    <div className="vw-pages">
      {Array.from({ length: pages.count }, (_, index) => (
        <Sheet key={index} pages={pages} number={index + 1} />
      ))}
    </div>
  );
}

// Sheet is a page of a PDF, which is drawn while it is near what is shown:
// a page that is drawn holds many megabytes.
function Sheet({ pages, number }: { pages: Pages; number: number }) {
  const canvas = useRef<HTMLCanvasElement>(null);
  const near = useNear(canvas);
  // How wide the page is for its height: that of the first page, until the
  // page is drawn and has its own.
  const [ratio, setRatio] = useState(pages.ratio);
  useEffect(() => {
    const node = canvas.current;
    if (!near || !node) return;
    // As many pixels as the screen has for the page.
    const dense = Math.min(window.devicePixelRatio || 1, 2);
    const drawing = pages.draw(number, node, node.clientWidth * dense);
    drawing.done.then(
      () => setRatio(node.width / node.height),
      () => {},
    );
    return () => {
      drawing.cancel();
      node.width = node.height = 0;
    };
  }, [pages, number, near]);
  return <canvas ref={canvas} style={{ aspectRatio: ratio }} />;
}

// Text shows what was read of a text, and says so if that is not all of it.
function Text({ file, data }: { file: Shown; data: Blob }) {
  const cut = file.size > data.size;
  const [text, setText] = useState<string>();
  useEffect(() => {
    let gone = false;
    // What was cut may end within a character.
    void data
      .text()
      .then(
        (read) => gone || setText(cut ? read.replace(/\uFFFD$/, "") : read),
      );
    return () => {
      gone = true;
    };
  }, [data, cut]);
  if (text === undefined) return <span className="ring" />;
  return (
    <div className="vw-text">
      <pre>{text}</pre>
      {cut && (
        <p>
          The first {size(data.size)} of {size(file.size)}.
        </p>
      )}
    </div>
  );
}
