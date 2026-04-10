"use client";

import { useEffect, useState } from "react";
import { useParams, useRouter } from "next/navigation";
import Link from "next/link";
import { ArrowLeft, Trash2, Settings, MessageSquare, AlertCircle } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { LoadingState } from "@/components/feedback/loading-state";
import { ErrorState } from "@/components/feedback/error-state";
import { agentsApi } from "@/features/agents/api/agents-api";
import { useAuth } from "@/features/auth/providers/auth-provider";
import { routes } from "@/lib/constants/routes";
import { formatTokens } from "@/lib/utils/format";
import type { Agent, McpToolBinding } from "@/types";

/**
 * Agent detail page — read-only overview of an agent's configuration.
 *
 * Layout: breadcrumb + actions row, hero header, info cards grid, footer
 * action bar. Deleted agents show a banner and disable session creation.
 */
export default function AgentDetailPage() {
  const { user } = useAuth();
  const params = useParams<{ id: string }>();
  const router = useRouter();
  const [agent, setAgent] = useState<Agent | null>(null);
  const [mcpTools, setMcpTools] = useState<McpToolBinding[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!user) return;
    Promise.all([agentsApi.get(params.id), agentsApi.getMcpTools(params.id)])
      .then(([agentData, bindings]) => {
        setAgent(agentData);
        setMcpTools(bindings);
      })
      .catch((err: Error) => setError(err.message))
      .finally(() => setLoading(false));
  }, [user, params.id]);

  if (loading) {
    return <LoadingState fullHeight label="Loading agent…" />;
  }

  if (error || !agent) {
    return (
      <div className="mx-auto max-w-container-sm p-8">
        <ErrorState title="Couldn't load agent" description={error || "Agent not found"} />
        <Link
          href={routes.agents}
          className="mt-4 inline-flex items-center gap-1.5 type-caption text-info hover:opacity-80"
        >
          <ArrowLeft size={12} strokeWidth={2} />
          Back to agents
        </Link>
      </div>
    );
  }

  const handleDelete = async () => {
    if (!confirm("Are you sure you want to delete this agent? This action cannot be undone.")) return;
    await agentsApi.remove(agent.id);
    router.push(routes.agents);
  };

  const handleNewSession = async () => {
    const session = await agentsApi.createSession(agent.id);
    router.push(routes.session(session.id));
  };

  return (
    <div className="h-full overflow-y-auto bg-canvas">
      <div className="mx-auto max-w-container px-8 py-8">
        {/* Breadcrumb + actions */}
        <div className="mb-6 flex items-center justify-between">
          <Link
            href={routes.agents}
            className="inline-flex items-center gap-1.5 type-caption text-fg-muted hover:text-fg-primary transition-colors"
          >
            <ArrowLeft size={12} strokeWidth={2} />
            Agents
          </Link>
          <div className="flex items-center gap-2">
            <Link href={routes.agentEdit(agent.id)}>
              <Button variant="ghost" size="sm">
                Edit
              </Button>
            </Link>
            <Link href={routes.agentSettings(agent.id)}>
              <Button variant="ghost" size="sm">
                <Settings size={13} strokeWidth={1.75} />
                Settings
              </Button>
            </Link>
            {agent.status !== "deleted" && (
              <Button variant="danger" size="sm" onClick={handleDelete}>
                <Trash2 size={13} strokeWidth={1.75} />
                Delete
              </Button>
            )}
          </div>
        </div>

        {/* Deleted banner */}
        {agent.status === "deleted" && (
          <div className="mb-6 flex items-center gap-2.5 rounded-md border border-danger/20 bg-danger/[0.04] px-4 py-3">
            <AlertCircle size={14} strokeWidth={2} className="shrink-0 text-danger" />
            <span className="type-caption text-danger">
              This agent has been deleted. Existing sessions remain active, but no new sessions can be created.
            </span>
          </div>
        )}

        {/* Hero header */}
        <div className="mb-8 flex items-start gap-4">
          <div
            className={`flex h-14 w-14 items-center justify-center rounded-lg bg-elevated shadow-2 text-2xl ${
              agent.status === "deleted" ? "grayscale" : ""
            }`}
          >
            {agent.icon || "🤖"}
          </div>
          <div className="flex-1">
            <h1 className="type-card-heading text-fg-primary">{agent.name}</h1>
            <p className="mt-1 type-body-tight text-fg-muted">
              {agent.description || "No description"}
            </p>
          </div>
        </div>

        {/* Info cards */}
        <div className="grid gap-4 md:grid-cols-2">
          <Card variant="elevated">
            <CardHeader>
              <CardTitle>Model</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <InfoRow label="Backend" value={agent.model_config.backend} />
              <InfoRow label="Model" value={agent.model_config.model} mono />
              {agent.model_config.max_output_tokens != null && (
                <InfoRow
                  label="Max Output Tokens"
                  value={formatTokens(agent.model_config.max_output_tokens)}
                />
              )}
            </CardContent>
          </Card>

          <Card variant="elevated">
            <CardHeader>
              <CardTitle>Configuration</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <InfoRow label="Identifier" value={agent.slug} mono />
              <InfoRow label="Visibility" value={agent.visibility} capitalize />
              <InfoRow label="Status" value={agent.status} capitalize />
            </CardContent>
          </Card>

          <Card variant="elevated" className="md:col-span-2">
            <CardHeader>
              <CardTitle>System Instruction</CardTitle>
            </CardHeader>
            <CardContent>
              <div className="rounded-md bg-canvas p-4 type-body-tight text-fg-secondary">
                <pre className="whitespace-pre-wrap font-sans">{agent.instruction}</pre>
              </div>
            </CardContent>
          </Card>

          <Card variant="elevated" className="md:col-span-2">
            <CardHeader>
              <CardTitle>Tools</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4">
              {agent.tools.length > 0 && (
                <div>
                  <p className="type-caption text-fg-muted mb-2">Built-in</p>
                  <div className="flex flex-wrap gap-2">
                    {agent.tools.map((tool) => (
                      <Badge key={tool} variant="muted">
                        {tool}
                      </Badge>
                    ))}
                  </div>
                </div>
              )}
              {mcpTools.length > 0 && (
                <div>
                  <p className="type-caption text-fg-muted mb-2">MCP Tools</p>
                  <div className="flex flex-wrap gap-2">
                    {mcpTools.map((b) => (
                      <Badge key={b.id} variant="info" title={b.tool.description || ""}>
                        {b.tool.qualified_name}
                      </Badge>
                    ))}
                  </div>
                </div>
              )}
              {agent.tools.length === 0 && mcpTools.length === 0 && (
                <p className="type-caption text-fg-muted">No tools configured</p>
              )}
            </CardContent>
          </Card>
        </div>

        {/* Footer actions */}
        <div className="mt-8 flex gap-3">
          <Button
            variant="cta"
            size="lg"
            className="flex-1"
            onClick={handleNewSession}
            disabled={agent.status === "deleted"}
          >
            <MessageSquare size={16} strokeWidth={1.75} />
            New Chat
          </Button>
          <Link href={routes.agentSessions(agent.id)} className="flex-1">
            <Button variant="secondary" size="lg" className="w-full">
              View All Sessions
            </Button>
          </Link>
        </div>
      </div>
    </div>
  );
}

function InfoRow({
  label,
  value,
  mono,
  capitalize: cap,
}: {
  label: string;
  value: string;
  mono?: boolean;
  capitalize?: boolean;
}) {
  return (
    <div className="flex items-center justify-between type-body-tight">
      <span className="text-fg-muted">{label}</span>
      <span
        className={`text-fg-secondary ${mono ? "font-mono type-code-sm" : ""} ${cap ? "capitalize" : ""}`}
      >
        {value}
      </span>
    </div>
  );
}
