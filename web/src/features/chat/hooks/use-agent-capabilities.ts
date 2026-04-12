"use client";

import { useEffect, useRef, useState } from "react";
import { agentsApi, modelsApi, type ModelOption } from "@/features/agents/api/agents-api";

interface AgentCapabilities {
  capabilities: string[];
  visionEnabled: boolean;
}

export function useAgentCapabilities(agentId: string | undefined): AgentCapabilities {
  const [capabilities, setCapabilities] = useState<string[]>([]);
  const fetchedRef = useRef<string | undefined>(undefined);

  useEffect(() => {
    if (!agentId || fetchedRef.current === agentId) return;
    fetchedRef.current = agentId;

    let cancelled = false;

    (async () => {
      try {
        const [agent, { models }] = await Promise.all([
          agentsApi.get(agentId),
          modelsApi.list(),
        ]);

        if (cancelled) return;

        const modelId = agent.model_config.model.includes("/")
          ? agent.model_config.model
          : `${agent.model_config.backend}/${agent.model_config.model}`;

        const match = models.find((m: ModelOption) => m.id === modelId);
        setCapabilities(match?.model_info?._ui?.capabilities ?? []);
      } catch {
        // Non-critical — degrade gracefully (no vision)
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [agentId]);

  return {
    capabilities,
    visionEnabled: capabilities.includes("vision"),
  };
}
