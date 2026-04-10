import { UserBubble } from "./user-bubble";
import { AgentBubble } from "./agent-bubble";
import type { Message } from "@/types";

/**
 * MessageBubble — dispatches to the appropriate bubble based on sender_type.
 * The two bubble types intentionally don't share a common shell because
 * their layouts are mirrored (left vs right) and their internal content
 * differs (markdown blocks vs plain text).
 */
export function MessageBubble({ message }: { message: Message }) {
  if (message.sender_type === "user") {
    return <UserBubble content={message.content} />;
  }
  return <AgentBubble message={message} />;
}
