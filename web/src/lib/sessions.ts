import type { Session, Turn } from "../api/types.ts";

// How a turn stands. Besides the states of the API it tells apart the
// moments of a running turn that look different: before its harness has the
// message, while it waits for a person, and after it was told to stop.
export type Status = Turn["state"] | "starting" | "waiting" | "stopping";

// How a session stands: as its newest turn, or "ended" for a session that
// has ended, or "none" for one without turns.
export type Standing = Status | "ended" | "none";

// What the console calls each way that a turn or a session stands.
export const STATUS_NAMES: Record<Standing, string> = {
  queued: "Queued",
  starting: "Starting",
  running: "Running",
  waiting: "Needs approval",
  stopping: "Stopping",
  completed: "Completed",
  failed: "Failed",
  cancelled: "Cancelled",
  expired: "Expired",
  ended: "Ended",
  none: "No messages yet",
};

export function status(turn: Turn): Status {
  if (turn.state !== "running") return turn.state;
  if (turn.cancel_requested_at) return "stopping";
  if (turn.approvals?.length) return "waiting";
  return turn.started_at ? "running" : "starting";
}

export function standing(session: Session): Standing {
  const turn = session.last_turn;
  if (turn && !turn.ended_at) return status(turn);
  if (session.ended_at) return "ended";
  return turn ? status(turn) : "none";
}

// standingText says in a word or two how a session stands. A session that
// expired says so.
export function standingText(session: Session): string {
  const stands = standing(session);
  if (stands === "ended" && session.end_reason === "expired") return "Expired";
  return STATUS_NAMES[stands];
}

// working reports whether a session takes room among the turns that run.
export function working(stands: Standing): boolean {
  return ["starting", "running", "waiting", "stopping"].includes(stands);
}
