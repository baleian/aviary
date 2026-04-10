interface UserBubbleProps {
  content: string;
}

/**
 * UserBubble — right-aligned user message with brand-tinted background.
 * Pure presentation, no markdown rendering (user input is plain text).
 */
export function UserBubble({ content }: UserBubbleProps) {
  return (
    <div className="flex flex-row-reverse gap-3 group animate-fade-in">
      <div className="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-info/15 type-small text-info">
        You
      </div>
      <div className="max-w-[75%]">
        <div className="rounded-xl rounded-tr-sm bg-info/10 px-4 py-3 type-body text-fg-primary">
          <div className="whitespace-pre-wrap break-words">{content}</div>
        </div>
      </div>
    </div>
  );
}
