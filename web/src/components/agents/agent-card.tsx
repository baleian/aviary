"use client";

import Link from "next/link";
import type { Agent } from "@/types";

interface AgentCardProps {
  agent: Agent;
}

const backendLabels: Record<string, string> = {
  claude: "Claude",
  ollama: "Ollama",
  vllm: "vLLM",
};

const visibilityConfig: Record<string, { label: string; className: string }> = {
  public: { label: "Public", className: "bg-success/10 text-success" },
  team: { label: "Team", className: "bg-warning/10 text-warning" },
  private: { label: "Private", className: "bg-secondary text-muted-foreground" },
};

export function AgentCard({ agent }: AgentCardProps) {
  const vis = visibilityConfig[agent.visibility] || visibilityConfig.private;

  return (
    <Link href={`/agents/${agent.id}`} className="group block">
      <div className="flex h-full flex-col rounded-xl border border-border/50 bg-card p-5 transition-all duration-200 hover:border-primary/30 hover:bg-card/80 hover:shadow-lg hover:shadow-primary/5">
        {/* Header */}
        <div className="mb-3 flex items-start justify-between">
          <div className="flex items-center gap-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-primary/10 text-lg">
              {agent.icon || "🤖"}
            </div>
            <div className="min-w-0">
              <h3 className="truncate text-sm font-semibold text-foreground group-hover:text-primary transition-colors">
                {agent.name}
              </h3>
              <span className="text-xs text-muted-foreground">
                {backendLabels[agent.model_config?.backend] || agent.model_config?.backend}
              </span>
            </div>
          </div>
          <span className={`shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium ${vis.className}`}>
            {vis.label}
          </span>
        </div>

        {/* Description */}
        <p className="mb-4 line-clamp-2 flex-1 text-sm leading-relaxed text-muted-foreground">
          {agent.description || "No description provided"}
        </p>

        {/* Footer */}
        <div className="flex items-center justify-between border-t border-border/40 pt-3">
          {agent.category ? (
            <span className="rounded-md bg-secondary px-2 py-0.5 text-[11px] text-muted-foreground">
              {agent.category}
            </span>
          ) : (
            <span />
          )}
          <span className="text-xs text-muted-foreground/60 group-hover:text-primary/60 transition-colors">
            Open →
          </span>
        </div>
      </div>
    </Link>
  );
}
