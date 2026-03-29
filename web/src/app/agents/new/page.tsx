"use client";

import { useRouter } from "next/navigation";
import Link from "next/link";
import { useAuth } from "@/components/providers/auth-provider";
import { AgentForm } from "@/components/agents/agent-form";
import { apiFetch } from "@/lib/api";

export default function NewAgentPage() {
  const { user, isLoading } = useAuth();
  const router = useRouter();

  if (isLoading || !user) {
    return (
      <div className="flex h-screen items-center justify-center">
        <div className="flex items-center gap-3 text-muted-foreground">
          <svg className="h-5 w-5 animate-spin" viewBox="0 0 24 24" fill="none">
            <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
            <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
          </svg>
          <span className="text-sm">Loading...</span>
        </div>
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
    <div className="min-h-screen">
      <nav className="sticky top-0 z-30 border-b border-border/50 glass">
        <div className="mx-auto flex max-w-3xl items-center px-6 py-3">
          <Link href="/agents" className="flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground transition-colors">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M19 12H5"/><polyline points="12 19 5 12 12 5"/></svg>
            Agents
          </Link>
        </div>
      </nav>

      <div className="mx-auto max-w-3xl px-6 py-8">
        <h1 className="mb-2 text-xl font-bold text-foreground">Create New Agent</h1>
        <p className="mb-8 text-sm text-muted-foreground">Configure your AI agent&apos;s behavior, model, and capabilities</p>
        <AgentForm onSubmit={handleSubmit} submitLabel="Create Agent" />
      </div>
    </div>
  );
}
