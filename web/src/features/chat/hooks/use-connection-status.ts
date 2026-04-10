"use client";

import { useEffect, useRef, useState } from "react";
import type { ConnectionStatus } from "@/lib/ws";

/**
 * useConnectionStatus — debounces intermediate "still connecting" states
 * so fast connections don't flicker their banner UI.
 *
 * Only the terminal states (ready / offline / disconnected) flip immediately.
 * Intermediate states (connecting / provisioning / etc) wait `delayMs`
 * before being surfaced.
 */
const IMMEDIATE = new Set<ConnectionStatus>(["ready", "offline", "disconnected"]);

export function useConnectionStatus(raw: ConnectionStatus, delayMs = 500) {
  const [visible, setVisible] = useState<ConnectionStatus | null>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (IMMEDIATE.has(raw)) {
      if (timerRef.current) {
        clearTimeout(timerRef.current);
        timerRef.current = null;
      }
      setVisible(raw);
      return;
    }

    if (!timerRef.current) {
      timerRef.current = setTimeout(() => {
        timerRef.current = null;
        setVisible(raw);
      }, delayMs);
    }

    return () => {
      if (timerRef.current) {
        clearTimeout(timerRef.current);
        timerRef.current = null;
      }
    };
  }, [raw, delayMs]);

  return visible;
}
