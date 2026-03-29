"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { useAuth } from "@/components/providers/auth-provider";
import { AgentCard } from "@/components/agents/agent-card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { apiFetch } from "@/lib/api";
import type { Agent } from "@/types";

interface AgentListResponse {
  items: Agent[];
  total: number;
}

export default function AgentsPage() {
  const { user, isLoading, logout } = useAuth();
  const router = useRouter();
  const [agents, setAgents] = useState<Agent[]>([]);
  const [total, setTotal] = useState(0);
  const [search, setSearch] = useState("");
  const [loadingAgents, setLoadingAgents] = useState(true);

  useEffect(() => {
    if (!isLoading && !user) {
      router.replace("/login");
    }
  }, [user, isLoading, router]);

  useEffect(() => {
    if (!user) return;
    setLoadingAgents(true);

    const path = search
      ? `/catalog/search?q=${encodeURIComponent(search)}`
      : "/agents";

    apiFetch<AgentListResponse>(path)
      .then((data) => {
        setAgents(data.items);
        setTotal(data.total);
      })
      .catch(() => {})
      .finally(() => setLoadingAgents(false));
  }, [user, search]);

  if (isLoading || !user) {
    return (
      <div className="flex h-screen items-center justify-center">
        <div className="text-muted-foreground">Loading...</div>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-7xl p-8">
      <header className="mb-8 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">Agent Catalog</h1>
          <p className="text-sm text-muted-foreground">
            {total} agent{total !== 1 ? "s" : ""} available
          </p>
        </div>
        <div className="flex items-center gap-4">
          {user.is_platform_admin && (
            <span className="rounded-full bg-primary px-3 py-1 text-xs text-primary-foreground">
              Admin
            </span>
          )}
          <Link href="/agents/new">
            <Button>Create Agent</Button>
          </Link>
          <Button variant="outline" size="sm" onClick={logout}>
            Sign out
          </Button>
        </div>
      </header>

      <div className="mb-6">
        <Input
          placeholder="Search agents..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="max-w-md"
        />
      </div>

      {loadingAgents ? (
        <div className="py-12 text-center text-muted-foreground">Loading agents...</div>
      ) : agents.length === 0 ? (
        <div className="rounded-lg border border-dashed p-12 text-center text-muted-foreground">
          {search ? "No agents match your search." : "No agents yet. Create your first agent!"}
        </div>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {agents.map((agent) => (
            <AgentCard key={agent.id} agent={agent} />
          ))}
        </div>
      )}
    </div>
  );
}
