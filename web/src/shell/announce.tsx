import { Link, useMatch } from "react-router";
import { controls, type ProjectContext } from "../api/project.ts";
import { useMe } from "../api/queries.ts";
import type { Session } from "../api/types.ts";
import { label } from "../lib/format.ts";
import { toast } from "../ui/toast.ts";

// The requests for approval that the caller has been told of.
const announced = new Set<string>();

// How long the caller is told of a request that waits, in ms.
const SHOWN = 9000;

// useAnnounce returns what tells the caller that a turn waits for an answer
// that the caller may give, wherever in the project the caller is. It is
// given each session of the project that changes. A request of the session
// in view shows in its conversation, and is not told of.
export function useAnnounce(
  context: ProjectContext | undefined,
): (session: Session) => void {
  const me = useMe().data;
  const viewed = useMatch("/p/:project/agents/:agent/s/:sid")?.params.sid;
  return (session) => {
    const turn = session.last_turn;
    if (!context || !me || !turn) return;
    const fresh = (turn.approvals ?? [])
      .map((request) => `${turn.id}/${request.id}`)
      .filter((request) => !announced.has(request));
    for (const request of fresh) announced.add(request);
    const agent = context.agents.find((each) => each.id === session.agent_id);
    if (!fresh.length || !agent || viewed === session.id) return;
    if (!controls(context, me, turn)) return;
    toast(
      <>
        <b>{label(agent)}</b> is waiting for you
      </>,
      "",
      SHOWN,
      <Link
        to={`/p/${context.project.slug}/agents/${agent.slug}/s/${session.id}`}
      >
        Review
      </Link>,
    );
  };
}
