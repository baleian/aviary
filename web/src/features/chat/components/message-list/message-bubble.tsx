import { UserBubble } from "./user-bubble";
import { AgentBubble } from "./agent-bubble";
import type { FileRef, Message } from "@/types";

interface MessageBubbleProps {
  message: Message;
  showAvatar?: boolean;
}

export function MessageBubble({ message, showAvatar = true }: MessageBubbleProps) {
  if (message.sender_type === "user") {
    const attachments = message.metadata?.attachments as FileRef[] | undefined;
    return (
      <UserBubble
        content={message.content}
        showAvatar={showAvatar}
        targetId={`${message.id}/user`}
        attachments={attachments}
      />
    );
  }
  return <AgentBubble message={message} showAvatar={showAvatar} />;
}
