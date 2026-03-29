"use client";

import { useRouter } from "next/navigation";
import { useAuth } from "@/components/providers/auth-provider";
import { AgentForm } from "@/components/agents/agent-form";
import { apiFetch } from "@/lib/api";

export default function NewAgentPage() {
  const { user, isLoading } = useAuth();
  const router = useRouter();

  if (isLoading || !user) {
    return (
      <div className="flex h-screen items-center justify-center">
        <div className="text-muted-foreground">Loading...</div>
      </div>
    );
  }

  const handleSubmit = async (data: any) => {
    const agent = await apiFetch<any>("/agents", {
      method: "POST",
      body: JSON.stringify(data),
    });
    router.push(`/agents/${agent.id}`);
  };

  return (
    <div className="mx-auto max-w-3xl p-8">
      <h1 className="mb-8 text-2xl font-bold">Create New Agent</h1>
      <AgentForm onSubmit={handleSubmit} submitLabel="Create Agent" />
    </div>
  );
}
