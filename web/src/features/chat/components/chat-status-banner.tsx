"use client";

import { Spinner } from "@/components/ui/spinner";
import type { ConnectionStatus } from "@/lib/ws";

const STATUS_LABELS: Record<ConnectionStatus, string> = {
  connecting: "Connecting",
  provisioning: "Setting up environment",
  spawning: "Starting agent",
  waiting: "Almost ready",
  ready: "Online",
  offline: "Offline",
  disconnected: "Disconnected",
};

interface ChatStatusBannerProps {
  status: ConnectionStatus;
  statusMessage: string | null;
  showConnecting: boolean;
}

/**
 * ChatStatusBanner — slim banner above the message list showing
 * connection state when not ready. Three rendered variants:
 *   - intermediate (warning, spinner)
 *   - offline      (danger)
 *   - disconnected (neutral, "refresh to reconnect")
 */
export function ChatStatusBanner({ status, statusMessage, showConnecting }: ChatStatusBannerProps) {
  if (showConnecting) {
    return (
      <div className="shrink-0 border-b border-warning/10 bg-warning/[0.04] px-6 py-2.5 animate-fade-in">
        <div className="mx-auto flex max-w-container-prose items-center justify-center gap-2">
          <Spinner size={14} className="text-warning" />
          <span className="type-caption text-warning">
            {STATUS_LABELS[status]}
            {statusMessage && ` — ${statusMessage}`}
          </span>
        </div>
      </div>
    );
  }

  if (status === "offline") {
    return (
      <div className="shrink-0 border-b border-danger/10 bg-danger/[0.04] px-6 py-2.5">
        <div className="mx-auto flex max-w-container-prose items-center justify-center">
          <span className="type-caption text-danger">
            Agent is offline{statusMessage && ` — ${statusMessage}`}
          </span>
        </div>
      </div>
    );
  }

  if (status === "disconnected") {
    return (
      <div className="shrink-0 border-b border-white/[0.06] bg-raised/50 px-6 py-2.5">
        <div className="mx-auto flex max-w-container-prose items-center justify-center">
          <span className="type-caption text-fg-muted">
            Connection lost. Refresh to reconnect.
          </span>
        </div>
      </div>
    );
  }

  return null;
}

export { STATUS_LABELS };
