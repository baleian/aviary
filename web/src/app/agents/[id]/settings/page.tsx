"use client";

import { useEffect, useState } from "react";
import { useParams, useRouter } from "next/navigation";
import Link from "next/link";
import { useAuth } from "@/components/providers/auth-provider";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { apiFetch } from "@/lib/api";
import type { Agent } from "@/types";

interface ACLEntry {
  id: string;
  agent_id: string;
  user_id: string | null;
  team_id: string | null;
  role: string;
  created_at: string;
}

export default function AgentSettingsPage() {
  const { user, isLoading } = useAuth();
  const params = useParams();
  const router = useRouter();
  const [agent, setAgent] = useState<Agent | null>(null);
  const [aclEntries, setAclEntries] = useState<ACLEntry[]>([]);
  const [loading, setLoading] = useState(true);

  // New ACL form
  const [newUserId, setNewUserId] = useState("");
  const [newRole, setNewRole] = useState("user");

  useEffect(() => {
    if (!isLoading && !user) {
      router.replace("/login");
      return;
    }
    if (!user) return;

    Promise.all([
      apiFetch<Agent>(`/agents/${params.id}`),
      apiFetch<{ items: ACLEntry[] }>(`/agents/${params.id}/acl`),
    ])
      .then(([agentData, aclData]) => {
        setAgent(agentData);
        setAclEntries(aclData.items);
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

  const handleAddACL = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newUserId.trim()) return;

    const entry = await apiFetch<ACLEntry>(`/agents/${agent.id}/acl`, {
      method: "POST",
      body: JSON.stringify({ user_id: newUserId, role: newRole }),
    });
    setAclEntries((prev) => [...prev, entry]);
    setNewUserId("");
  };

  const handleDeleteACL = async (aclId: string) => {
    await apiFetch(`/agents/${agent.id}/acl/${aclId}`, { method: "DELETE" });
    setAclEntries((prev) => prev.filter((a) => a.id !== aclId));
  };

  return (
    <div className="mx-auto max-w-3xl p-8">
      <Link href={`/agents/${agent.id}`} className="text-sm text-muted-foreground hover:underline">
        &larr; Back to {agent.name}
      </Link>
      <h1 className="mb-8 mt-2 text-2xl font-bold">Agent Settings</h1>

      <Card className="mb-8">
        <CardHeader>
          <CardTitle className="text-base">Access Control</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          {aclEntries.length > 0 ? (
            <div className="space-y-2">
              {aclEntries.map((entry) => (
                <div
                  key={entry.id}
                  className="flex items-center justify-between rounded-md border p-3"
                >
                  <div className="text-sm">
                    <span className="font-medium">
                      {entry.user_id ? `User: ${entry.user_id}` : `Team: ${entry.team_id}`}
                    </span>
                    <span className="ml-2 rounded bg-muted px-2 py-0.5 text-xs capitalize">
                      {entry.role}
                    </span>
                  </div>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => handleDeleteACL(entry.id)}
                  >
                    Remove
                  </Button>
                </div>
              ))}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">No access entries yet.</p>
          )}

          <form onSubmit={handleAddACL} className="flex items-end gap-3 pt-4 border-t">
            <div className="flex-1 space-y-2">
              <Label htmlFor="user_id">User ID</Label>
              <Input
                id="user_id"
                value={newUserId}
                onChange={(e) => setNewUserId(e.target.value)}
                placeholder="Enter user UUID"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="role">Role</Label>
              <Select
                id="role"
                value={newRole}
                onChange={(e) => setNewRole(e.target.value)}
              >
                <option value="viewer">Viewer</option>
                <option value="user">User</option>
                <option value="admin">Admin</option>
                <option value="owner">Owner</option>
              </Select>
            </div>
            <Button type="submit">Add</Button>
          </form>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Credentials</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-muted-foreground">
            Credential management will be available in Phase 4.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
