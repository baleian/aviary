"use client";

import Link from "next/link";
import type { Agent } from "@/types";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";

interface AgentCardProps {
  agent: Agent;
}

const backendLabels: Record<string, string> = {
  claude: "Claude API",
  ollama: "Ollama",
  vllm: "vLLM",
};

const visibilityLabels: Record<string, string> = {
  public: "Public",
  team: "Team",
  private: "Private",
};

export function AgentCard({ agent }: AgentCardProps) {
  return (
    <Card className="flex flex-col">
      <CardHeader>
        <div className="flex items-start justify-between">
          <div className="flex items-center gap-2">
            {agent.icon && <span className="text-2xl">{agent.icon}</span>}
            <CardTitle className="text-lg">{agent.name}</CardTitle>
          </div>
          <span className="rounded-full bg-secondary px-2 py-0.5 text-xs text-secondary-foreground">
            {visibilityLabels[agent.visibility] || agent.visibility}
          </span>
        </div>
        <CardDescription className="line-clamp-2">
          {agent.description || "No description"}
        </CardDescription>
      </CardHeader>
      <CardContent className="mt-auto flex items-center justify-between">
        <div className="flex gap-2">
          <span className="rounded bg-muted px-2 py-0.5 text-xs">
            {backendLabels[agent.model_config?.backend] || agent.model_config?.backend}
          </span>
          {agent.category && (
            <span className="rounded bg-muted px-2 py-0.5 text-xs">{agent.category}</span>
          )}
        </div>
        <div className="flex gap-2">
          <Link href={`/agents/${agent.id}`}>
            <Button variant="outline" size="sm">Open</Button>
          </Link>
        </div>
      </CardContent>
    </Card>
  );
}
