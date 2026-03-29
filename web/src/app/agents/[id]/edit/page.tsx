"use client";

import { useEffect, useState } from "react";
import { useParams, useRouter } from "next/navigation";
import Link from "next/link";
import { useAuth } from "@/components/providers/auth-provider";
import { AgentForm } from "@/components/agents/agent-form";
import { apiFetch } from "@/lib/api";
import type { Agent } from "@/types";

export default function EditAgentPage() {
  const { user, isLoading } = useAuth();
  const params = useParams();
  const router = useRouter();
  const [agent, setAgent] = useState<Agent | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!isLoading && !user) {
      router.replace("/login");
      return;
    }
    if (!user) return;

    apiFetch<Agent>(`/agents/${params.id}`)
      .then(setAgent)
      .catch(() => {})
      .finally(() => setLoading(false));
  }, [user, isLoading, params.id, router]);

  if (isLoading || loading || !agent) {
    return (
      <div className="flex h-screen items-center justify-center">
        <div className="text-muted-foreground">Loading...</div>
      </div>
    );
  }

  const handleSubmit = async (data: any) => {
    await apiFetch(`/agents/${agent.id}`, {
      method: "PUT",
      body: JSON.stringify(data),
    });
    router.push(`/agents/${agent.id}`);
  };

  return (
    <div className="mx-auto max-w-3xl p-8">
      <Link href={`/agents/${agent.id}`} className="text-sm text-muted-foreground hover:underline">
        &larr; Back to {agent.name}
      </Link>
      <h1 className="mb-8 mt-2 text-2xl font-bold">Edit Agent</h1>
      <AgentForm
        initialData={{
          name: agent.name,
          slug: agent.slug,
          description: agent.description || "",
          instruction: agent.instruction,
          model_config: agent.model_config as any,
          tools: agent.tools as string[],
          visibility: agent.visibility,
          category: agent.category || "",
        }}
        onSubmit={handleSubmit}
        submitLabel="Save Changes"
      />
    </div>
  );
}
