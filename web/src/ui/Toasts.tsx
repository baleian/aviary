import { cls } from "../lib/cls.ts";
import { Icon } from "./Icon.tsx";
import { dismiss, useToasts } from "./toast.ts";

const GLYPHS = { "": "info", ok: "check", bad: "warn" } as const;

// Toasts shows the messages of toast().
export function Toasts() {
  const toasts = useToasts();
  return (
    <div className="toasts" role="status" aria-live="polite">
      {toasts.map(({ id, message, kind, action }) => (
        <div key={id} className={cls("toast", kind)}>
          <Icon name={GLYPHS[kind]} />
          <span>{message}</span>
          {action && <span onClick={() => dismiss(id)}>{action}</span>}
        </div>
      ))}
    </div>
  );
}
