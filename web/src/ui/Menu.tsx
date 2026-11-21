import {
  createContext,
  type ReactNode,
  type RefObject,
  useContext,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { createPortal } from "react-dom";
import { Link, useLocation } from "react-router";
import { cls } from "../lib/cls.ts";
import { Icon } from "./Icon.tsx";
import type { IconName } from "./icons.ts";

const Close = createContext<() => void>(() => {});

// What Menu gives the button that opens it.
export interface MenuButton {
  ref: RefObject<HTMLButtonElement | null>;
  "aria-haspopup": "true";
  "aria-expanded": boolean;
  onClick: () => void;
}

// Menu is a button and the menu that it opens below itself, or above if
// there is no room below. button draws the button with what it is given.
// The menu closes when one of its items is chosen, on a press outside it, on
// Escape, and when the page goes elsewhere.
export function Menu({
  button,
  align = "left",
  className,
  children,
}: {
  button: (props: MenuButton) => ReactNode;
  align?: "left" | "right";
  className?: string;
  children: ReactNode;
}) {
  const location = useLocation();
  // The menu is open at the location at which it was opened.
  const [openedAt, setOpenedAt] = useState<string | null>(null);
  const open = openedAt === location.key;
  const anchor = useRef<HTMLButtonElement>(null);
  const pop = useRef<HTMLDivElement>(null);

  useLayoutEffect(() => {
    if (!open || !anchor.current || !pop.current) return;
    const box = anchor.current.getBoundingClientRect();
    const width = pop.current.offsetWidth;
    const height = pop.current.offsetHeight;
    let left = align === "right" ? box.right - width : box.left;
    left = Math.max(8, Math.min(left, window.innerWidth - width - 8));
    let top = box.bottom + 8;
    const above = box.top - height - 8;
    if (top + height > window.innerHeight - 8 && above > 8) top = above;
    pop.current.style.left = left + "px";
    pop.current.style.top = top + "px";
  }, [open, align]);

  useEffect(() => {
    if (!open) return;
    const close = () => setOpenedAt(null);
    const press = (event: PointerEvent) => {
      const target = event.target as Node;
      const inside =
        pop.current?.contains(target) || anchor.current?.contains(target);
      if (!inside) close();
    };
    const key = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.stopPropagation();
      close();
      anchor.current?.focus();
    };
    document.addEventListener("pointerdown", press, true);
    document.addEventListener("keydown", key, true);
    window.addEventListener("resize", close);
    return () => {
      document.removeEventListener("pointerdown", press, true);
      document.removeEventListener("keydown", key, true);
      window.removeEventListener("resize", close);
    };
  }, [open]);

  return (
    <>
      {button({
        ref: anchor,
        "aria-haspopup": "true",
        "aria-expanded": open,
        onClick: () => setOpenedAt(open ? null : location.key),
      })}
      {open &&
        createPortal(
          <div ref={pop} className={cls("pop", className)} role="menu">
            <Close value={() => setOpenedAt(null)}>{children}</Close>
          </div>,
          document.body,
        )}
    </>
  );
}

// MenuItem is a row of a menu: a link if it has a "to", and a button
// otherwise.
export function MenuItem({
  text,
  small,
  glyph,
  lead,
  on,
  danger,
  disabled,
  to,
  onClick,
}: {
  text: ReactNode;
  // A line below the text.
  small?: ReactNode;
  glyph?: IconName;
  // What comes before the text instead of an icon.
  lead?: ReactNode;
  // Whether the row is the one in effect.
  on?: boolean;
  danger?: boolean;
  disabled?: boolean;
  to?: string;
  onClick?: () => void;
}) {
  const close = useContext(Close);
  const className = cls("it", on && "on", danger && "dng");
  const content = (
    <>
      {lead}
      {glyph && <Icon name={glyph} />}
      {small ? (
        <span>
          {text}
          <small>{small}</small>
        </span>
      ) : (
        <span className="nm">{text}</span>
      )}
      {on && <Icon name="check" className="end" />}
    </>
  );
  if (to) {
    return (
      <Link className={className} to={to} role="menuitem">
        {content}
      </Link>
    );
  }
  return (
    <button
      className={className}
      type="button"
      role="menuitem"
      disabled={disabled}
      onClick={() => {
        close();
        onClick?.();
      }}
    >
      {content}
    </button>
  );
}
