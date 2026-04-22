"use client";

import { useEffect, useRef } from "react";
import { createPortal } from "react-dom";
import { X } from "@/components/icons";
import { cn } from "@/lib/utils";

interface DialogProps {
  open: boolean;
  onClose: () => void;
  title: React.ReactNode;
  description?: React.ReactNode;
  children: React.ReactNode;
  footer?: React.ReactNode;
  widthClassName?: string;
}

/**
 * Glass dialog with focus trap, ESC-to-close, scroll lock, and overlay
 * click-dismiss. Mounted via portal so it can sit above stacking contexts
 * (sidebar, page content).
 */
export function Dialog({
  open,
  onClose,
  title,
  description,
  children,
  footer,
  widthClassName = "max-w-[560px]",
}: DialogProps) {
  const surfaceRef = useRef<HTMLDivElement>(null);
  const prevFocusRef = useRef<HTMLElement | null>(null);

  // Focus trap + ESC + scroll lock.
  useEffect(() => {
    if (!open) return;
    prevFocusRef.current = document.activeElement as HTMLElement | null;
    const origOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    // Focus the surface after mount so initial Tab navigates inside.
    requestAnimationFrame(() => {
      surfaceRef.current?.focus();
    });

    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        e.stopPropagation();
        onClose();
      } else if (e.key === "Tab") {
        // Simple trap: if focus would leave the surface, cycle.
        const root = surfaceRef.current;
        if (!root) return;
        const focusables = root.querySelectorAll<HTMLElement>(
          'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
        );
        if (focusables.length === 0) {
          e.preventDefault();
          return;
        }
        const first = focusables[0];
        const last = focusables[focusables.length - 1];
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault();
          first.focus();
        }
      }
    }
    document.addEventListener("keydown", onKey);
    return () => {
      document.body.style.overflow = origOverflow;
      document.removeEventListener("keydown", onKey);
      prevFocusRef.current?.focus?.();
    };
  }, [open, onClose]);

  if (!open) return null;
  if (typeof document === "undefined") return null;

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4 animate-fade-in-fast"
      aria-modal="true"
      role="dialog"
    >
      <div
        className="absolute inset-0 bg-black/60 backdrop-blur-[2px]"
        onClick={onClose}
        aria-hidden
      />
      <div
        ref={surfaceRef}
        tabIndex={-1}
        className={cn(
          "relative w-full overflow-hidden rounded-lg",
          "bg-popover border border-white/[0.08] shadow-5",
          "focus:outline-none",
          widthClassName,
        )}
      >
        <header className="flex items-start justify-between gap-4 border-b border-white/[0.06] px-6 py-4">
          <div className="min-w-0">
            <h2 className="type-card-heading text-fg-primary">{title}</h2>
            {description && (
              <p className="mt-1 type-caption text-fg-muted">{description}</p>
            )}
          </div>
          <button
            type="button"
            onClick={onClose}
            className="-mr-1 rounded-sm p-1 text-fg-muted hover:bg-white/[0.06] hover:text-fg-primary transition-colors"
            aria-label="Close dialog"
          >
            <X size={14} strokeWidth={1.75} />
          </button>
        </header>

        <div className="max-h-[70vh] overflow-y-auto px-6 py-5">{children}</div>

        {footer && (
          <footer className="flex items-center justify-end gap-2 border-t border-white/[0.06] px-6 py-3">
            {footer}
          </footer>
        )}
      </div>
    </div>,
    document.body,
  );
}
