"use client";

import { useCallback, useEffect, useRef } from "react";
import { usePathname, useSearchParams } from "next/navigation";
import { useNotificationsPush } from "./notifications-provider";
import { useUserEvents } from "@/features/events/user-events-provider";
import { routes } from "@/lib/constants/routes";

export function useGlobalReplyNotifier() {
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const push = useNotificationsPush();

  const viewingRef = useRef<{ pathname: string; sessionParam: string | null }>({
    pathname,
    sessionParam: searchParams.get("session"),
  });
  useEffect(() => {
    viewingRef.current = {
      pathname,
      sessionParam: searchParams.get("session"),
    };
  }, [pathname, searchParams]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    if (!("Notification" in window)) return;
    if (Notification.permission === "default") {
      Notification.requestPermission().catch(() => {});
    }
  }, []);

  useUserEvents(
    useCallback(
      (event) => {
        if (event.type !== "session_changed") return;
        if (event.terminal !== "done") return;

        const sessionId = event.session_id;
        const { pathname: curPath, sessionParam } = viewingRef.current;
        const isVisible =
          typeof document === "undefined" || document.visibilityState === "visible";
        const viewingThisSession =
          isVisible &&
          ((sessionParam === sessionId && curPath.startsWith("/agents/")) ||
            curPath === routes.session(sessionId));
        if (viewingThisSession) return;

        const title = event.title || "Agent reply";
        const agentId = event.agent_id ?? null;
        const href = agentId
          ? routes.agentChat(agentId, sessionId)
          : routes.session(sessionId);

        push({
          kind: "chat_reply",
          title,
          description: "Your agent finished responding.",
          href,
          tone_id: agentId ?? sessionId,
        });

        if (
          typeof window !== "undefined" &&
          "Notification" in window &&
          Notification.permission === "granted"
        ) {
          try {
            const native = new Notification(title, {
              body: "Your agent finished responding.",
              icon: "/favicon.ico",
              tag: sessionId,
            });
            native.onclick = () => {
              window.focus();
              window.location.href = href;
              native.close();
            };
          } catch {}
        }
      },
      [push],
    ),
  );
}
