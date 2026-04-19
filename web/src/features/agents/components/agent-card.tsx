"use client";

import Link from "next/link";
import { Badge } from "@/components/ui/badge";
import { Spinner } from "@/components/ui/spinner";
import { useCreateSession } from "@/features/agents/hooks/use-create-session";
import { routes } from "@/lib/constants/routes";
import { cn } from "@/lib/utils";
import type { Agent } from "@/types";

interface AgentCardProps {
  agent: Agent;
  deleted?: boolean;
}

/**
 * AgentCard — Aurora Glass tile.
 *
 * Glass pane that lifts + picks up a violet glow on hover. "Start chat"
 * is the aurora-gradient CTA; it preventDefaults the wrapping Link so
 * the card click still takes the user to the agent detail page.
 */
export function AgentCard({ agent, deleted }: AgentCardProps) {
  const { createAndNavigate, creating, error } = useCreateSession(agent.id);

  const handleStartChat = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    void createAndNavigate();
  };

  return (
    <Link href={routes.agent(agent.id)} className="group block">
      <article
        className={cn(
          "relative flex h-full flex-col rounded-lg p-5",
          "glass-pane shadow-2",
          "transition-all duration-[320ms] ease-[cubic-bezier(0.16,1,0.3,1)]",
          deleted
            ? "opacity-50 hover:opacity-70"
            : "hover:-translate-y-[1px] hover:bg-white/[0.07] hover:shadow-[0_8px_32px_rgba(0,0,0,0.45),0_0_0_1px_rgba(123,92,255,0.3),inset_0_1px_0_rgba(255,255,255,0.08),0_0_40px_rgba(123,92,255,0.18)]",
        )}
      >
        {/* Header */}
        <div className="mb-3 flex items-start justify-between gap-2">
          <div className="flex items-center gap-3 min-w-0">
            <div
              className={cn(
                "flex h-11 w-11 shrink-0 items-center justify-center rounded-md text-lg",
                deleted ? "bg-white/[0.05] grayscale" : "bg-aurora-a-soft border border-white/[0.08]",
              )}
            >
              {agent.icon || "🤖"}
            </div>
            <div className="min-w-0">
              <h3
                className={cn(
                  "type-body truncate transition-colors",
                  deleted
                    ? "text-fg-muted line-through decoration-fg-disabled"
                    : "text-fg-primary group-hover:text-aurora-violet",
                )}
              >
                {agent.name}
              </h3>
              <p className="type-caption text-fg-muted">
                {agent.model_config?.backend}
              </p>
            </div>
          </div>

          {deleted && <Badge variant="danger">Deleted</Badge>}
        </div>

        {/* Description */}
        <p className="mb-4 line-clamp-2 flex-1 type-body-tight text-fg-muted">
          {agent.description || "No description provided"}
        </p>

        {/* Footer */}
        <div className="flex items-center justify-end gap-3 border-t border-white/[0.06] pt-3">
          {deleted ? (
            <span className="type-caption text-fg-disabled">View sessions →</span>
          ) : (
            <button
              type="button"
              onClick={handleStartChat}
              disabled={creating}
              aria-label={`Start chat with ${agent.name}`}
              className={cn(
                "inline-flex items-center gap-1.5 rounded-sm px-2.5 py-1 type-caption-bold",
                "bg-white/[0.05] border border-white/[0.08] text-fg-secondary",
                "transition-colors duration-200 ease-out",
                "hover:bg-white/[0.09] hover:border-white/[0.14] hover:text-fg-primary",
                "disabled:opacity-50 disabled:cursor-not-allowed",
                "focus-visible:outline-none focus-visible:border-aurora-cyan/40 focus-visible:shadow-[0_0_0_3px_rgba(79,201,255,0.15)]",
              )}
            >
              {creating ? (
                <>
                  <Spinner size={11} className="text-fg-secondary" />
                  Starting…
                </>
              ) : (
                <>
                  Start chat
                  <span className="text-fg-muted">↵</span>
                </>
              )}
            </button>
          )}
        </div>

        {/* Inline error — stop click bubbling so it doesn't trigger card navigation */}
        {error && (
          <div
            className="mt-2 type-caption text-danger"
            role="alert"
            onClick={(e) => e.stopPropagation()}
          >
            {error}
          </div>
        )}
      </article>
    </Link>
  );
}
