"use client";

import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { X } from "@/components/icons";
import { mcpApi } from "@/features/agents/api/mcp-api";
import { ToolRow } from "./tool-row";
import type { McpServerInfo, McpToolInfo } from "@/types";

interface ToolSelectorProps {
  selectedToolIds: string[];
  onChange: (toolIds: string[], toolMap: Map<string, McpToolInfo>) => void;
  open: boolean;
  onClose: () => void;
}

/**
 * ToolSelector — modal dialog for browsing and selecting MCP tools.
 *
 * Two modes:
 *   - Browse:  expandable list of MCP servers, lazy-loading their tools
 *   - Search:  debounced full-text search across all tools
 */
export function ToolSelector({ selectedToolIds, onChange, open, onClose }: ToolSelectorProps) {
  const [servers, setServers] = useState<McpServerInfo[]>([]);
  const [serverTools, setServerTools] = useState<Record<string, McpToolInfo[]>>({});
  const [searchQuery, setSearchQuery] = useState("");
  const [searchResults, setSearchResults] = useState<McpToolInfo[] | null>(null);
  const [expandedServer, setExpandedServer] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [allToolInfo, setAllToolInfo] = useState<Map<string, McpToolInfo>>(new Map());
  const selected = new Set(selectedToolIds);

  useEffect(() => {
    if (!open) return;
    setLoading(true);
    mcpApi
      .listServers()
      .then((data) => setServers(data))
      .finally(() => setLoading(false));
  }, [open]);

  const loadServerTools = useCallback(
    async (serverId: string) => {
      if (serverTools[serverId]) return;
      const tools = await mcpApi.listServerTools(serverId);
      setServerTools((prev) => ({ ...prev, [serverId]: tools }));
      setAllToolInfo((prev) => {
        const next = new Map(prev);
        for (const t of tools) next.set(t.id, t);
        return next;
      });
    },
    [serverTools],
  );

  const handleToggle = (toolId: string) => {
    const next = new Set(selected);
    if (next.has(toolId)) next.delete(toolId);
    else next.add(toolId);
    onChange(Array.from(next), allToolInfo);
  };

  // Debounced search
  useEffect(() => {
    if (!searchQuery.trim()) {
      setSearchResults(null);
      return;
    }
    const timer = setTimeout(async () => {
      const results = await mcpApi.searchTools(searchQuery);
      setSearchResults(results);
      setAllToolInfo((prev) => {
        const next = new Map(prev);
        for (const t of results) next.set(t.id, t);
        return next;
      });
    }, 300);
    return () => clearTimeout(timer);
  }, [searchQuery]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm animate-fade-in-fast p-6">
      <div className="w-full max-w-2xl max-h-[80vh] rounded-xl bg-elevated shadow-5 flex flex-col">
        <div className="flex items-center justify-between border-b border-white/[0.06] px-6 py-4">
          <h2 className="type-button text-fg-primary">Browse Tools</h2>
          <button
            type="button"
            onClick={onClose}
            className="text-fg-muted hover:text-fg-primary transition-colors"
            aria-label="Close"
          >
            <X size={18} strokeWidth={2} />
          </button>
        </div>

        <div className="px-6 py-3 border-b border-white/[0.06]">
          <Input
            placeholder="Search tools by name or description…"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
          />
        </div>

        <div className="flex-1 overflow-y-auto px-6 py-4 space-y-3">
          {loading && (
            <div className="flex items-center gap-2 type-caption text-fg-muted">
              <Spinner size={12} />
              Loading…
            </div>
          )}

          {searchResults !== null ? (
            searchResults.length === 0 ? (
              <p className="type-caption text-fg-muted">No tools found.</p>
            ) : (
              <div className="space-y-1">
                {searchResults.map((tool) => (
                  <ToolRow
                    key={tool.id}
                    tool={tool}
                    checked={selected.has(tool.id)}
                    onToggle={handleToggle}
                  />
                ))}
              </div>
            )
          ) : (
            servers.map((srv) => (
              <div key={srv.id} className="rounded-md border border-white/[0.06]">
                <button
                  type="button"
                  className="w-full flex items-center justify-between px-4 py-3 text-left hover:bg-white/[0.03] rounded-md transition-colors"
                  onClick={() => {
                    const next = expandedServer === srv.id ? null : srv.id;
                    setExpandedServer(next);
                    if (next) loadServerTools(next);
                  }}
                >
                  <div>
                    <span className="type-body text-fg-primary">{srv.name}</span>
                    {srv.description && (
                      <span className="ml-2 type-caption text-fg-muted">{srv.description}</span>
                    )}
                  </div>
                  <span className="type-caption text-fg-disabled">{srv.tool_count} tools</span>
                </button>
                {expandedServer === srv.id && (
                  <div className="border-t border-white/[0.06] px-4 py-2 space-y-1">
                    {(serverTools[srv.id] ?? []).length === 0 ? (
                      <p className="type-caption text-fg-muted py-2">Loading tools…</p>
                    ) : (
                      (serverTools[srv.id] ?? []).map((tool) => (
                        <ToolRow
                          key={tool.id}
                          tool={tool}
                          checked={selected.has(tool.id)}
                          onToggle={handleToggle}
                        />
                      ))
                    )}
                  </div>
                )}
              </div>
            ))
          )}
        </div>

        <div className="flex items-center justify-between border-t border-white/[0.06] px-6 py-4">
          <span className="type-caption text-fg-muted">{selected.size} tool(s) selected</span>
          <Button variant="primary" size="sm" onClick={onClose}>
            Done
          </Button>
        </div>
      </div>
    </div>
  );
}
