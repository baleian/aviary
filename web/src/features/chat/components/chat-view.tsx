"use client";

import { useCallback, useRef } from "react";
import Link from "next/link";
import { useChatMessages } from "@/features/chat/hooks/use-chat-messages";
import { useConnectionStatus } from "@/features/chat/hooks/use-connection-status";
import { useTitleEditor } from "@/features/chat/hooks/use-title-editor";
import { useChatExport } from "@/features/chat/hooks/use-chat-export";
import { ChatWidthProvider, useChatWidth } from "@/features/chat/hooks/use-chat-width";
import { ChatHeader } from "./chat-header";
import { ChatStatusBanner } from "./chat-status-banner";
import { MessageList } from "./message-list/message-list";
import { ChatInput } from "./input/chat-input";
import { TodoPanel } from "./todos/todo-panel";
import { LoadingState } from "@/components/feedback/loading-state";
import { routes } from "@/lib/constants/routes";

/**
 * ChatView — assembles the chat experience for a session.
 *
 * This component is intentionally a thin orchestrator: it composes hooks
 * and child components but contains no business logic of its own. The
 * old 570-line page is fully decomposed into hooks/components under
 * features/chat.
 *
 * The width preference (narrow / comfort / wide) is owned by
 * ChatWidthProvider so the header, banner, message list, and input all
 * stay aligned without prop-drilling.
 */
export function ChatView({ sessionId }: { sessionId: string }) {
  return (
    <ChatWidthProvider>
      <ChatViewInner sessionId={sessionId} />
    </ChatWidthProvider>
  );
}

function ChatViewInner({ sessionId }: { sessionId: string }) {
  const { widthClass } = useChatWidth();
  const chat = useChatMessages(sessionId);
  const visibleStatus = useConnectionStatus(chat.status);
  const scrollRef = useRef<HTMLDivElement>(null);

  const titleEditor = useTitleEditor({
    session: chat.session,
    patchSession: chat.patchSession,
  });

  const exportFns = useChatExport({
    containerRef: scrollRef,
    messages: chat.messages,
    session: chat.session,
  });

  const handleSend = useCallback(
    (content: string) => {
      if (chat.send(content)) {
        titleEditor.setAutoTitleFromMessage(content);
      }
    },
    [chat, titleEditor],
  );

  if (chat.loading) {
    return <LoadingState fullHeight label="Loading session…" />;
  }

  if (!chat.session) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-4">
        <p className="type-body text-fg-muted">Session not found</p>
        <Link href={routes.agents} className="type-caption text-info hover:opacity-80">
          Back to agents
        </Link>
      </div>
    );
  }

  const isReady = chat.status === "ready";
  const isInputDisabled = !isReady || chat.isStreaming;
  const displayStatus = visibleStatus ?? "ready";
  // Banner shows "Connecting…" only for fresh first-connect intermediates.
  // Reconnecting / offline / disconnected get their own dedicated banners.
  const showConnecting =
    !isReady &&
    visibleStatus !== null &&
    visibleStatus !== "ready" &&
    visibleStatus !== "offline" &&
    visibleStatus !== "disconnected" &&
    visibleStatus !== "reconnecting";

  return (
    <div className="flex h-full flex-col bg-canvas">
      <ChatHeader
        session={chat.session}
        status={displayStatus}
        hasMessages={chat.messages.length > 0}
        onPrintVisual={exportFns.printVisual}
        onExportText={exportFns.exportText}
        titleEditor={titleEditor}
      />

      <ChatStatusBanner
        status={displayStatus}
        statusMessage={chat.statusMessage}
        showConnecting={showConnecting}
        reconnectIn={chat.reconnectIn}
        onRetryNow={chat.retryNow}
      />

      <MessageList
        ref={scrollRef}
        messages={chat.messages}
        blocks={chat.blocks}
        isStreaming={chat.isStreaming}
        isReady={isReady}
        hasMore={chat.hasMore}
        loadingEarlier={chat.loadingEarlier}
        onLoadEarlier={chat.loadEarlier}
      />

      <div className="shrink-0 border-t border-white/[0.06] px-6 py-4">
        <div className={`mx-auto ${widthClass} space-y-2`}>
          {chat.todos.length > 0 && <TodoPanel todos={chat.todos} />}
          <ChatInput
            onSend={handleSend}
            onCancel={chat.cancel}
            disabled={isInputDisabled}
            isStreaming={chat.isStreaming}
            placeholder={
              !isReady ? "Waiting for agent…" : chat.isStreaming ? "Agent is responding…" : undefined
            }
            agentId={chat.session.agent_id}
          />
        </div>
      </div>
    </div>
  );
}
