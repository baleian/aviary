"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Wrench, X } from "@/components/icons";
import { FormSection } from "./form-section";
import { ToolSelector } from "@/features/agents/components/tool-selector/tool-selector";
import type { McpToolInfo } from "@/types";
import type { AgentFormData } from "./types";

interface ToolsSectionProps {
  data: AgentFormData;
  setField: <K extends keyof AgentFormData>(key: K, value: AgentFormData[K]) => void;
  toolInfoMap: Map<string, McpToolInfo>;
  setToolInfoMap: (map: Map<string, McpToolInfo>) => void;
}

export function ToolsSection({ data, setField, toolInfoMap, setToolInfoMap }: ToolsSectionProps) {
  const [pickerOpen, setPickerOpen] = useState(false);

  return (
    <FormSection title="Tools & Integrations" description="Connect external tools via MCP servers">
      <Card variant="standard" className="p-5 space-y-4">
        {data.mcp_tool_ids.length > 0 ? (
          <div className="flex flex-wrap gap-2">
            {data.mcp_tool_ids.map((id) => {
              const info = toolInfoMap.get(id);
              const label = info?.qualified_name || id.slice(0, 8);
              return (
                <span
                  key={id}
                  className="inline-flex items-center gap-1.5 rounded-sm bg-info/10 px-2.5 py-1 type-caption text-info ring-1 ring-inset ring-info/20"
                  title={info?.description ?? undefined}
                >
                  <Wrench size={11} strokeWidth={2} />
                  {label}
                  <button
                    type="button"
                    onClick={() => {
                      setField("mcp_tool_ids", data.mcp_tool_ids.filter((t) => t !== id));
                    }}
                    className="ml-0.5 text-info/60 hover:text-info"
                    aria-label={`Remove ${label}`}
                  >
                    <X size={10} strokeWidth={2.5} />
                  </button>
                </span>
              );
            })}
          </div>
        ) : (
          <p className="type-caption text-fg-muted">No tools connected yet.</p>
        )}

        <Button type="button" variant="secondary" size="sm" onClick={() => setPickerOpen(true)}>
          Browse Tools
        </Button>
      </Card>

      <ToolSelector
        selectedToolIds={data.mcp_tool_ids}
        onChange={(ids, map) => {
          setField("mcp_tool_ids", ids);
          if (map) setToolInfoMap(map);
        }}
        open={pickerOpen}
        onClose={() => setPickerOpen(false)}
      />
    </FormSection>
  );
}
