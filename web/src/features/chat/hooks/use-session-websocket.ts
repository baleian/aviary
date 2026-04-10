"use client";

import { useEffect, useRef, useState } from "react";
import { openSessionSocket } from "@/lib/ws";
import type { ConnectionStatus, WSMessage } from "@/lib/ws";

interface UseSessionWebSocketOptions {
  sessionId: string | undefined;
  enabled: boolean;
  onMessage: (msg: WSMessage) => void;
}

interface UseSessionWebSocketResult {
  ws: WebSocket | null;
  status: ConnectionStatus;
  statusMessage: string | null;
}

/**
 * useSessionWebSocket — owns the WebSocket lifecycle for a session.
 *
 * Listens for `status` messages internally and exposes the current
 * `ConnectionStatus` to the caller. All other messages are forwarded
 * verbatim to `onMessage`.
 *
 * Strict-mode safe via the `connectedRef` guard.
 */
export function useSessionWebSocket({
  sessionId,
  enabled,
  onMessage,
}: UseSessionWebSocketOptions): UseSessionWebSocketResult {
  const [status, setStatus] = useState<ConnectionStatus>("connecting");
  const [statusMessage, setStatusMessage] = useState<string | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const connectedRef = useRef(false);
  const onMessageRef = useRef(onMessage);

  // Keep latest onMessage in a ref so we don't reconnect on every render
  useEffect(() => {
    onMessageRef.current = onMessage;
  }, [onMessage]);

  useEffect(() => {
    if (!sessionId || !enabled) return;
    if (connectedRef.current) return;
    connectedRef.current = true;

    setStatus("connecting");
    setStatusMessage(null);

    let cancelled = false;

    openSessionSocket(sessionId, {
      onMessage: (msg) => {
        if (cancelled) return;
        if (msg.type === "status") {
          setStatus(msg.status);
          setStatusMessage(msg.message || null);
        }
        onMessageRef.current(msg);
      },
      onClose: () => {
        if (cancelled) return;
        setStatus("disconnected");
        // Forward a synthetic status so consumers (use-chat-messages) can
        // reset their streaming state without a separate handler.
        onMessageRef.current({ type: "status", status: "disconnected" });
      },
    })
      .then((ws) => {
        if (cancelled) {
          ws.close();
          return;
        }
        wsRef.current = ws;
      })
      .catch(() => {
        if (!cancelled) setStatus("disconnected");
      });

    return () => {
      cancelled = true;
      wsRef.current?.close();
      wsRef.current = null;
      connectedRef.current = false;
    };
  }, [sessionId, enabled]);

  return { ws: wsRef.current, status, statusMessage };
}
