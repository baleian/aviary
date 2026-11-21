import {
  type FormEvent,
  type ReactNode,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";
import { cls } from "../lib/cls.ts";
import { sentence } from "../lib/format.ts";
import { Icon } from "./Icon.tsx";

// Dialog is a dialog with a form. onSubmit runs when the form is sent: the
// dialog shows the message of what it throws, and closes with what it
// returns. onClose gets that, or null if the dialog was dismissed.
//
// The focus goes to the field marked data-autofocus, or else to the first
// field, or else to the button that sends the form.
export function Dialog<T>({
  title,
  lead,
  action = "Save",
  danger,
  size,
  onSubmit,
  onClose,
  children,
}: {
  title: ReactNode;
  // A line below the title.
  lead?: ReactNode;
  // The text of the button that sends the form.
  action?: string;
  danger?: boolean;
  size?: "wide" | "slim";
  onSubmit: (data: FormData) => T | Promise<T>;
  onClose: (result: T | null) => void;
  children?: ReactNode;
}) {
  const heading = useId();
  const dialog = useRef<HTMLDialogElement>(null);
  const send = useRef<HTMLButtonElement>(null);
  const [error, setError] = useState("");
  const [sending, setSending] = useState(false);

  useEffect(() => {
    const node = dialog.current;
    if (!node || node.open) return;
    node.showModal();
    const first =
      node.querySelector<HTMLElement>("[data-autofocus]") ??
      node.querySelector<HTMLElement>(
        ".dlg-b input, .dlg-b textarea, .dlg-b select",
      ) ??
      send.current;
    first?.focus();
  }, []);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (sending) return;
    setError("");
    setSending(true);
    try {
      onClose(await onSubmit(new FormData(event.currentTarget)));
    } catch (failure) {
      setError(sentence(failure instanceof Error ? failure.message : ""));
      setSending(false);
    }
  }

  return (
    <dialog
      ref={dialog}
      className={cls("dlg", size)}
      aria-labelledby={heading}
      onCancel={(event) => {
        event.preventDefault();
        onClose(null);
      }}
      onPointerDown={(event) => {
        // A press on the backdrop is a press on the dialog itself.
        if (event.target === event.currentTarget) onClose(null);
      }}
    >
      <form noValidate onSubmit={(event) => void submit(event)}>
        <div className="dlg-h">
          <div>
            <h2 id={heading}>{title}</h2>
            {lead && <p>{lead}</p>}
          </div>
          <button
            className="ib"
            type="button"
            title="Close"
            onClick={() => onClose(null)}
          >
            <Icon name="x" className="lg" />
          </button>
        </div>
        <div className="dlg-b">
          {error && (
            <div className="alert bad" role="alert">
              <Icon name="warn" />
              <span>{error}</span>
            </div>
          )}
          {children}
        </div>
        <div className="dlg-f">
          <button
            className="btn grey"
            type="button"
            onClick={() => onClose(null)}
          >
            Cancel
          </button>
          <button
            ref={send}
            className={cls("btn", danger ? "dng" : "pri")}
            type="submit"
            disabled={sending}
          >
            {sending && <span className="ring" />}
            {action}
          </button>
        </div>
      </form>
    </dialog>
  );
}
