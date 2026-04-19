import { Sparkles } from "@/components/icons";

/**
 * ChatEmptyState — pre-conversation moment. Aurora-tinted glass orb
 * announces that the agent is ready; body copy stays quiet so the
 * backdrop colour carries the emotion.
 */
export function ChatEmptyState() {
  return (
    <div className="flex flex-col items-center justify-center py-24 animate-fade-in">
      <div className="relative">
        <div className="flex h-16 w-16 items-center justify-center rounded-2xl glass-raised shadow-3 text-fg-primary">
          <Sparkles size={24} strokeWidth={1.5} className="text-aurora-violet" />
        </div>
        <div
          aria-hidden="true"
          className="absolute inset-0 -z-10 rounded-2xl bg-aurora-a blur-2xl opacity-40"
        />
      </div>
      <p className="mt-5 type-subheading text-fg-primary">Ready when you are</p>
      <p className="mt-1 type-caption text-fg-muted">Send a message to start the conversation</p>
    </div>
  );
}
