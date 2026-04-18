import type { NodeData, NodeType, WorkflowEdge, WorkflowNode } from "./types";

// --- Plan operations (mirror api/app/schemas/workflow_assistant.py) ---

export interface AddNodeOp {
  op: "add_node";
  id: string;
  type: NodeType;
  position: { x: number; y: number };
  data: Record<string, unknown>;
}

export interface UpdateNodeOp {
  op: "update_node";
  id: string;
  data_patch: Record<string, unknown>;
}

export interface DeleteNodeOp {
  op: "delete_node";
  id: string;
}

export interface AddEdgeOp {
  op: "add_edge";
  id?: string;
  source: string;
  target: string;
  sourceHandle?: string;
  targetHandle?: string;
}

export interface DeleteEdgeOp {
  op: "delete_edge";
  id: string;
}

export type PlanOp = AddNodeOp | UpdateNodeOp | DeleteNodeOp | AddEdgeOp | DeleteEdgeOp;

// --- Chat message shape used by the assistant panel ---

import type { StreamBlock } from "@/types";

export interface AssistantChatMessage {
  id: string;
  role: "user" | "assistant";
  /** User messages only. Assistant content is rendered from `blocks`. */
  content: string;
  /** Live-streamed blocks for the assistant's turn. */
  blocks?: StreamBlock[];
  plan?: PlanOp[];
  planStatus?: "pending" | "accepted" | "rejected";
  error?: string;
  /** True while the assistant turn is still streaming. */
  streaming?: boolean;
}

// --- Validation (mirrors server-side _validate_plan_references) ---

export function validatePlan(
  plan: PlanOp[],
  nodes: WorkflowNode[],
  edges: WorkflowEdge[],
): string | null {
  const liveNodes = new Set(nodes.map((n) => n.id));
  const liveEdges = new Set(edges.map((e) => e.id));

  for (let i = 0; i < plan.length; i++) {
    const op = plan[i];
    switch (op.op) {
      case "add_node":
        if (liveNodes.has(op.id)) return `step[${i}] add_node id '${op.id}' already exists`;
        liveNodes.add(op.id);
        break;
      case "update_node":
        if (!liveNodes.has(op.id)) return `step[${i}] update_node id '${op.id}' does not exist`;
        break;
      case "delete_node":
        if (!liveNodes.has(op.id)) return `step[${i}] delete_node id '${op.id}' does not exist`;
        liveNodes.delete(op.id);
        break;
      case "add_edge":
        if (!liveNodes.has(op.source)) return `step[${i}] add_edge source '${op.source}' does not exist`;
        if (!liveNodes.has(op.target)) return `step[${i}] add_edge target '${op.target}' does not exist`;
        if (op.id) {
          if (liveEdges.has(op.id)) return `step[${i}] add_edge id '${op.id}' conflicts`;
          liveEdges.add(op.id);
        }
        break;
      case "delete_edge":
        if (!liveEdges.has(op.id)) return `step[${i}] delete_edge id '${op.id}' does not exist`;
        liveEdges.delete(op.id);
        break;
    }
  }
  return null;
}

// --- Apply the plan to a (nodes, edges) pair → new (nodes, edges) ---

export function applyPlan(
  plan: PlanOp[],
  nodes: WorkflowNode[],
  edges: WorkflowEdge[],
): { nodes: WorkflowNode[]; edges: WorkflowEdge[] } {
  let n: WorkflowNode[] = nodes.map((node) => ({ ...node }));
  let e: WorkflowEdge[] = edges.map((edge) => ({ ...edge }));

  for (const op of plan) {
    switch (op.op) {
      case "add_node": {
        const newNode: WorkflowNode = {
          id: op.id,
          type: op.type,
          position: op.position,
          data: op.data as NodeData,
        };
        n = [...n, newNode];
        break;
      }
      case "update_node":
        n = n.map((node) =>
          node.id === op.id
            ? {
                ...node,
                data: {
                  ...(node.data as Record<string, unknown>),
                  ...op.data_patch,
                } as NodeData,
              }
            : node,
        );
        break;
      case "delete_node":
        n = n.filter((node) => node.id !== op.id);
        e = e.filter((edge) => edge.source !== op.id && edge.target !== op.id);
        break;
      case "add_edge": {
        const edgeId = op.id ?? `xy-edge__${op.source}-${op.target}-${Date.now()}`;
        const newEdge: WorkflowEdge = {
          id: edgeId,
          source: op.source,
          target: op.target,
          ...(op.sourceHandle != null && { sourceHandle: op.sourceHandle }),
          ...(op.targetHandle != null && { targetHandle: op.targetHandle }),
        };
        e = [...e, newEdge];
        break;
      }
      case "delete_edge":
        e = e.filter((edge) => edge.id !== op.id);
        break;
    }
  }
  return { nodes: n, edges: e };
}

// --- Human-readable summary of a single op, for the preview UI ---

export interface PlanOpSummary {
  tone: "add" | "modify" | "remove";
  verb: string;
  detail: string;
}

export function describePlanOp(op: PlanOp): PlanOpSummary {
  switch (op.op) {
    case "add_node": {
      const label = (op.data as { label?: string }).label;
      const name = label ? `"${label}"` : op.id;
      return { tone: "add", verb: "Add node", detail: `${op.type} ${name}` };
    }
    case "update_node": {
      const keys = Object.keys(op.data_patch);
      const preview = keys.length <= 3 ? keys.join(", ") : `${keys.slice(0, 3).join(", ")} +${keys.length - 3}`;
      return { tone: "modify", verb: "Modify", detail: `${op.id} — ${preview || "(empty patch)"}` };
    }
    case "delete_node":
      return { tone: "remove", verb: "Remove node", detail: op.id };
    case "add_edge":
      return { tone: "add", verb: "Connect", detail: `${op.source} → ${op.target}` };
    case "delete_edge":
      return { tone: "remove", verb: "Disconnect", detail: `edge ${op.id}` };
  }
}
