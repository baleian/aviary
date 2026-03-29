"use client";

import { useEffect, useState } from "react";
import { useParams, useRouter } from "next/navigation";
import Link from "next/link";
import { useAuth } from "@/components/providers/auth-provider";
import { Button } from "@/components/ui/button";
import { apiFetch } from "@/lib/api";
import type { Agent, Session } from "@/types";

export default function AgentSessionsPage() {
  const { user, isLoading } = useAuth();
  const params = useParams();
  const router = useRouter();
  const [agent, setAgent] = useState<Agent | null>(null);
  const [sessions, setSessions] = useState<Session[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!isLoading && !user) {
      router.replace("/login");
      return;
    }
    if (!user) return;

    Promise.all([
      apiFetch<Agent>(`/agents/${params.id}`),
      apiFetch<{ items: Session[] }>(`/agents/${params.id}/sessions`).catch(() => ({ items: [] })),
    ])
      .then(([agentData, sessionsData]) => {
        setAgent(agentData);
        setSessions(sessionsData.items);
      })
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

  const handleNewSession = async () => {
    const session = await apiFetch<Session>(`/agents/${agent.id}/sessions`, {
      method: "POST",
      body: JSON.stringify({ type: "private" }),
    });
    router.push(`/sessions/${session.id}`);
  };

  return (
    <div className="mx-auto max-w-3xl p-8">
      <Link href={`/agents/${agent.id}`} className="text-sm text-muted-foreground hover:underline">
        &larr; Back to {agent.name}
      </Link>
      <div className="mb-6 mt-2 flex items-center justify-between">
        <h1 className="text-2xl font-bold">Sessions</h1>
        <Button onClick={handleNewSession}>New Session</Button>
      </div>

      {sessions.length === 0 ? (
        <div className="rounded-lg border border-dashed p-12 text-center text-muted-foreground">
          No sessions yet. Start a new chat session!
        </div>
      ) : (
        <div className="space-y-2">
          {sessions.map((session) => (
            <Link
              key={session.id}
              href={`/sessions/${session.id}`}
              className="block rounded-lg border p-4 hover:bg-accent"
            >
              <div className="flex items-center justify-between">
                <div>
                  <p className="font-medium">{session.title || "Untitled Session"}</p>
                  <p className="text-sm text-muted-foreground">
                    {session.type} &middot; {new Date(session.created_at).toLocaleDateString()}
                  </p>
                </div>
                <span
                  className={`rounded-full px-2 py-0.5 text-xs ${
                    session.status === "active"
                      ? "bg-green-100 text-green-700"
                      : "bg-muted text-muted-foreground"
                  }`}
                >
                  {session.status}
                </span>
              </div>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
