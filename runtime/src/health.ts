/**
 * Health and readiness probes for the agent runtime.
 *
 * Capacity is reported by the server via setCapacityProbe() so this module
 * does not import SessionManager directly.
 */

import { Router, type Request, type Response } from "express";

interface CapacityProbe {
  hasCapacity: boolean;
  activeCount: number;
}

let _ready = false;
let _capacityProbe: (() => CapacityProbe) | null = null;

export function setReady(ready = true): void {
  _ready = ready;
}

export function setCapacityProbe(probe: () => CapacityProbe): void {
  _capacityProbe = probe;
}

export const healthRouter = Router();

healthRouter.get("/health", (_req: Request, res: Response) => {
  res.json({ status: "ok" });
});

healthRouter.get("/ready", (_req: Request, res: Response) => {
  if (!_ready) {
    res.status(503).json({ status: "not_ready" });
    return;
  }
  if (!_capacityProbe) {
    throw new Error("Health: capacity probe not registered");
  }
  const probe = _capacityProbe();
  if (!probe.hasCapacity) {
    res.status(503).json({ status: "at_capacity", active: probe.activeCount });
    return;
  }
  res.json({ status: "ready", active: probe.activeCount });
});
