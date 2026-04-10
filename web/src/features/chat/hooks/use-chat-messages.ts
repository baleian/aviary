"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { http } from "@/lib/http";
import { sendWsMessage } from "@/lib/ws";
import type { WSMessage } from "@/lib/ws";
import type { Message, Session } from "@/types";
import { useStreamingBlocks } from "./use-streaming-blocks";
import { useSessionWebSocket } from "./use-session-websocket";

interface SessionDetail {
  session: Session;
  messages: Message[];
}

interface UseChatMessagesResult {
  session: Session | null;
  messages: Message[];
  loading: boolean;
  isStreaming: boolean;
  blocks: ReturnType<typeof useStreamingBlocks>["blocks"];
  todos: ReturnType<typeof useStreamingBlocks>["todos"];
  status: ReturnType<typeof useSessionWebSocket>["status"];
  statusMessage: ReturnType<typeof useSessionWebSocket>["statusMessage"];
  reconnectIn: ReturnType<typeof useSessionWebSocket>["reconnectIn"];
  retryNow: ReturnType<typeof useSessionWebSocket>["retryNow"];
  send: (content: string) => boolean;
  cancel: () => void;
  patchSession: (patch: Partial<Session>) => void;
}

/**
 * useChatMessages — composes the chat data flow:
 *   1. Initial fetch of session + history
 *   2. WS subscription for live deltas
 *   3. Streaming block accumulation
 *   4. Translation of WS events into the persistent `messages` array
 *
 * The component using this hook just needs to render — no state machinery.
 */
export function useChatMessages(sessionId: string): UseChatMessagesResult {
  const [session, setSession] = useState<Session | null>(null);
  const [messages, setMessages] = useState<Message[]>([]);
  const [loading, setLoading] = useState(true);
  const [isStreaming, setIsStreaming] = useState(false);
  const blockState = useStreamingBlocks();

  const reloadHistory = useCallback(async () => {
    const data = await http.get<SessionDetail>(`/sessions/${sessionId}`);
    setMessages(data.messages);
    return data;
  }, [sessionId]);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    http
      .get<SessionDetail>(`/sessions/${sessionId}`)
      .then((data) => {
        if (cancelled) return;
        setSession(data.session);
        setMessages(data.messages);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [sessionId]);

  // Stable handler that avoids stale closures via refs
  const blockStateRef = useRef(blockState);
  useEffect(() => {
    blockStateRef.current = blockState;
  }, [blockState]);

  const handleMessage = useCallback(
    (msg: WSMessage) => {
      const bs = blockStateRef.current;
      switch (msg.type) {
        case "status":
          if (msg.status === "disconnected") {
            // Connection lost — flush in-flight state and reload from DB so any
            // partially saved response surfaces correctly.
            bs.reset();
            setIsStreaming(false);
            reloadHistory().catch(() => {
              /* reload failure is non-fatal — UI shows last known state */
            });
          }
          break;

        case "user_message":
          setMessages((prev) => [
            ...prev,
            {
              id: crypto.randomUUID(),
              session_id: sessionId,
              sender_type: "user",
              sender_id: msg.sender_id,
              content: msg.content,
              metadata: {},
              created_at: new Date().toISOString(),
            },
          ]);
          break;

        case "thinking":
        case "chunk":
        case "tool_use":
        case "tool_result":
        case "tool_progress":
          bs.handleMessage(msg);
          break;

        case "done": {
          bs.finalize();
          const finalContent = bs.flattenText();
          const savedBlocks = bs.getBlocksMeta();
          const metadata: Record<string, unknown> =
            savedBlocks.length > 0 ? { blocks: savedBlocks } : {};
          bs.reset();

          if (finalContent || savedBlocks.length > 0) {
            setMessages((prev) => {
              if (prev.some((m) => m.id === msg.messageId)) return prev;
              return [
                ...prev,
                {
                  id: msg.messageId,
                  session_id: sessionId,
                  sender_type: "agent",
                  content: finalContent,
                  metadata,
                  created_at: new Date().toISOString(),
                },
              ];
            });
          }
          setIsStreaming(false);
          break;
        }

        case "error":
          bs.reset();
          setIsStreaming(false);
          setMessages((prev) => [
            ...prev,
            {
              id: crypto.randomUUID(),
              session_id: sessionId,
              sender_type: "agent",
              content: `Error: ${msg.message}`,
              metadata: {},
              created_at: new Date().toISOString(),
            },
          ]);
          break;

        case "replay_start":
          setIsStreaming(true);
          bs.reset();
          break;

        case "replay_end":
          break;

        case "cancelled": {
          // Don't finalize — leave any running tools as-is so they show cancelled state
          const partialContent = bs.flattenText();
          const partialBlocks = bs.getBlocksMeta();
          const cancelMeta: Record<string, unknown> = { cancelled: true };
          if (partialBlocks.length > 0) cancelMeta.blocks = partialBlocks;
          bs.reset();
          if (partialContent || partialBlocks.length > 0) {
            const cancelledId = msg.messageId || crypto.randomUUID();
            setMessages((prev) => {
              if (prev.some((m) => m.id === cancelledId)) return prev;
              return [
                ...prev,
                {
                  id: cancelledId,
                  session_id: sessionId,
                  sender_type: "agent",
                  content: partialContent,
                  metadata: cancelMeta,
                  created_at: new Date().toISOString(),
                },
              ];
            });
          }
          setIsStreaming(false);
          break;
        }

        case "stream_complete":
          setMessages((prev) => {
            if (prev.some((m) => m.id === msg.messageId)) return prev;
            return [
              ...prev,
              {
                id: msg.messageId,
                session_id: sessionId,
                sender_type: "agent",
                content: msg.content,
                metadata: {},
                created_at: new Date().toISOString(),
              },
            ];
          });
          setIsStreaming(false);
          break;
      }
    },
    [sessionId, reloadHistory],
  );

  const { ws, status, statusMessage, reconnectIn, retryNow } = useSessionWebSocket({
    sessionId,
    enabled: !!session,
    onMessage: handleMessage,
    // After every successful (re)connect, refresh history so anything that
    // happened during the offline window appears in the UI.
    onReconnected: () => {
      reloadHistory().catch(() => {
        /* non-fatal */
      });
    },
  });

  const send = useCallback(
    (content: string) => {
      if (status !== "ready") return false;
      const ok = sendWsMessage(ws, { type: "message", content });
      if (ok) setIsStreaming(true);
      return ok;
    },
    [ws, status],
  );

  const cancel = useCallback(() => {
    sendWsMessage(ws, { type: "cancel" });
  }, [ws]);

  const patchSession = useCallback((patch: Partial<Session>) => {
    setSession((prev) => (prev ? { ...prev, ...patch } : prev));
  }, []);

  return {
    session,
    messages,
    loading,
    isStreaming,
    blocks: blockState.blocks,
    todos: blockState.todos,
    status,
    statusMessage,
    reconnectIn,
    retryNow,
    send,
    cancel,
    patchSession,
  };
}
