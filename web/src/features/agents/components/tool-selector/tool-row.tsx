"use client";

import type { McpToolInfo } from "@/types";

interface ToolRowProps {
  tool: McpToolInfo;
  checked: boolean;
  onToggle: (id: string) => void;
}

export function ToolRow({ tool, checked, onToggle }: ToolRowProps) {
  return (
    <label className="flex items-start gap-3 px-3 py-2 rounded-sm hover:bg-white/[0.03] cursor-pointer transition-colors">
      <input
        type="checkbox"
        checked={checked}
        onChange={() => onToggle(tool.id)}
        className="mt-0.5 rounded border-white/20 bg-canvas accent-info"
      />
      <div className="flex-1 min-w-0">
        <div className="type-body-tight text-fg-primary truncate">{tool.qualified_name}</div>
        {tool.description && (
          <p className="type-caption text-fg-muted line-clamp-2 mt-0.5">{tool.description}</p>
        )}
      </div>
    </label>
  );
}
