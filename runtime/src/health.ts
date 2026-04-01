/**
 * Health and readiness probes for the agent runtime.
 */

import { Router, type Request, type Response } from "express";
import type { SessionManager } from "./session-manager.js";

let _ready = false;
let _manager: SessionManager | null = null;

export function setReady(ready = true): void {
  _ready = ready;
}

export function setManager(mgr: SessionManager): void {
  _manager = mgr;
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
  if (_manager && !_manager.hasCapacity) {
    res.status(503).json({ status: "at_capacity", active: _manager.activeCount });
    return;
  }
  res.json({ status: "ready", active: _manager?.activeCount ?? 0 });
});
