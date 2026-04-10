"use client";

import Link from "next/link";
import { Badge } from "@/components/ui/badge";
import { routes } from "@/lib/constants/routes";
import { cn } from "@/lib/utils";
import type { Agent } from "@/types";

interface AgentCardProps {
  agent: Agent;
  deleted?: boolean;
}

const BACKEND_LABELS: Record<string, string> = {
  claude: "Claude",
  ollama: "Ollama",
  vllm: "vLLM",
};

const VISIBILITY_VARIANTS: Record<Agent["visibility"], "success" | "warning" | "muted"> = {
  public: "success",
  team: "warning",
  private: "muted",
};

const VISIBILITY_LABELS: Record<Agent["visibility"], string> = {
  public: "Public",
  team: "Team",
  private: "Private",
};

/**
 * AgentCard — single tile in the agents grid. Uses the Level-2 double-ring
 * shadow for elevated containment instead of a flat border + shadow.
 */
export function AgentCard({ agent, deleted }: AgentCardProps) {
  return (
    <Link href={routes.agent(agent.id)} className="group block">
      <article
        className={cn(
          "flex h-full flex-col rounded-lg p-5 transition-all duration-200",
          "bg-elevated shadow-2",
          deleted
            ? "opacity-50 hover:opacity-70"
            : "hover:glow-warm",
        )}
      >
        {/* Header */}
        <div className="mb-3 flex items-start justify-between gap-2">
          <div className="flex items-center gap-3 min-w-0">
            <div
              className={cn(
                "flex h-10 w-10 shrink-0 items-center justify-center rounded-md text-lg",
                deleted ? "bg-raised grayscale" : "bg-raised",
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
                    : "text-fg-primary group-hover:text-brand",
                )}
              >
                {agent.name}
              </h3>
              <p className="type-caption text-fg-muted">
                {BACKEND_LABELS[agent.model_config?.backend] || agent.model_config?.backend}
              </p>
            </div>
          </div>

          {deleted ? (
            <Badge variant="danger">Deleted</Badge>
          ) : (
            <Badge variant={VISIBILITY_VARIANTS[agent.visibility]}>
              {VISIBILITY_LABELS[agent.visibility]}
            </Badge>
          )}
        </div>

        {/* Description */}
        <p className="mb-4 line-clamp-2 flex-1 type-body-tight text-fg-muted">
          {agent.description || "No description provided"}
        </p>

        {/* Footer */}
        <div className="flex items-center justify-between border-t border-white/[0.06] pt-3">
          {agent.category ? (
            <Badge variant="muted">{agent.category}</Badge>
          ) : (
            <span />
          )}
          <span
            className={cn(
              "type-caption transition-colors",
              deleted ? "text-fg-disabled" : "text-fg-muted group-hover:text-brand",
            )}
          >
            {deleted ? "View sessions →" : "Open →"}
          </span>
        </div>
      </article>
    </Link>
  );
}
