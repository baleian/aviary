"use client";

import { forwardRef } from "react";
import { MessageBubble } from "./message-bubble";
import { StreamingResponse } from "@/features/chat/components/blocks/streaming-response";
import { ChatEmptyState } from "@/features/chat/components/chat-empty-state";
import type { Message, StreamBlock } from "@/types";

interface MessageListProps {
  messages: Message[];
  blocks: StreamBlock[];
  isStreaming: boolean;
  isReady: boolean;
}

/**
 * MessageList — scrollable message container with empty state and
 * live streaming response. Forward-refs the scroll element so the parent
 * can attach scroll-to-bottom and chat export hooks.
 */
export const MessageList = forwardRef<HTMLDivElement, MessageListProps>(
  function MessageList({ messages, blocks, isStreaming, isReady }, ref) {
    return (
      <div ref={ref} className="flex-1 overflow-y-auto">
        <div className="mx-auto max-w-container-prose px-6 py-6">
          {messages.length === 0 && isReady && !isStreaming && <ChatEmptyState />}

          <div className="space-y-5">
            {messages.map((msg) => (
              <MessageBubble key={msg.id} message={msg} />
            ))}

            {(blocks.length > 0 || isStreaming) && (
              <StreamingResponse blocks={blocks} isStreaming={isStreaming} />
            )}
          </div>
        </div>
      </div>
    );
  },
);
