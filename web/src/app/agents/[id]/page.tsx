"use client";

import { useEffect, useState } from "react";
import { useParams, useRouter } from "next/navigation";
import Link from "next/link";
import { useAuth } from "@/components/providers/auth-provider";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { apiFetch } from "@/lib/api";
import type { Agent } from "@/types";

export default function AgentDetailPage() {
  const { user, isLoading } = useAuth();
  const params = useParams();
  const router = useRouter();
  const [agent, setAgent] = useState<Agent | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!isLoading && !user) {
      router.replace("/login");
      return;
    }
    if (!user) return;

    apiFetch<Agent>(`/agents/${params.id}`)
      .then(setAgent)
      .catch((err) => setError(err.message))
      .finally(() => setLoading(false));
  }, [user, isLoading, params.id, router]);

  if (isLoading || loading) {
    return (
      <div className="flex h-screen items-center justify-center">
        <div className="text-muted-foreground">Loading...</div>
      </div>
    );
  }

  if (error || !agent) {
    return (
      <div className="mx-auto max-w-3xl p-8">
        <div className="rounded-md bg-destructive/10 p-4 text-destructive">
          {error || "Agent not found"}
        </div>
        <Link href="/agents" className="mt-4 inline-block text-primary underline">
          Back to agents
        </Link>
      </div>
    );
  }

  const handleDelete = async () => {
    if (!confirm("Are you sure you want to delete this agent?")) return;
    await apiFetch(`/agents/${agent.id}`, { method: "DELETE" });
    router.push("/agents");
  };

  return (
    <div className="mx-auto max-w-4xl p-8">
      <div className="mb-6 flex items-center justify-between">
        <div>
          <Link href="/agents" className="text-sm text-muted-foreground hover:underline">
            &larr; Back to agents
          </Link>
          <h1 className="mt-2 text-2xl font-bold">{agent.name}</h1>
          <p className="text-muted-foreground">{agent.description}</p>
        </div>
        <div className="flex gap-2">
          <Link href={`/agents/${agent.id}/edit`}>
            <Button variant="outline">Edit</Button>
          </Link>
          <Link href={`/agents/${agent.id}/settings`}>
            <Button variant="outline">Settings</Button>
          </Link>
          <Button variant="destructive" onClick={handleDelete}>
            Delete
          </Button>
        </div>
      </div>

      <div className="grid gap-6 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Model Configuration</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            <div className="flex justify-between">
              <span className="text-muted-foreground">Backend</span>
              <span>{agent.model_config.backend}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-muted-foreground">Model</span>
              <span>{agent.model_config.model}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-muted-foreground">Temperature</span>
              <span>{agent.model_config.temperature}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-muted-foreground">Max Tokens</span>
              <span>{agent.model_config.maxTokens}</span>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">Details</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            <div className="flex justify-between">
              <span className="text-muted-foreground">Slug</span>
              <span>{agent.slug}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-muted-foreground">Visibility</span>
              <span className="capitalize">{agent.visibility}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-muted-foreground">Status</span>
              <span className="capitalize">{agent.status}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-muted-foreground">Namespace</span>
              <span>{agent.namespace || "—"}</span>
            </div>
          </CardContent>
        </Card>

        <Card className="md:col-span-2">
          <CardHeader>
            <CardTitle className="text-base">System Instruction</CardTitle>
          </CardHeader>
          <CardContent>
            <pre className="whitespace-pre-wrap rounded-md bg-muted p-4 text-sm">
              {agent.instruction}
            </pre>
          </CardContent>
        </Card>

        <Card className="md:col-span-2">
          <CardHeader>
            <CardTitle className="text-base">Tools</CardTitle>
          </CardHeader>
          <CardContent>
            {agent.tools.length > 0 ? (
              <div className="flex flex-wrap gap-2">
                {agent.tools.map((tool: string) => (
                  <span key={tool} className="rounded bg-muted px-2 py-1 text-sm">
                    {tool}
                  </span>
                ))}
              </div>
            ) : (
              <p className="text-sm text-muted-foreground">No tools configured</p>
            )}
          </CardContent>
        </Card>
      </div>

      <div className="mt-8">
        <Link href={`/agents/${agent.id}/sessions`}>
          <Button size="lg" className="w-full">
            Start Chat Session
          </Button>
        </Link>
      </div>
    </div>
  );
}
