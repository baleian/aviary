"use client";

import * as React from "react";
import { DEFAULT_ROUTE, Route, deserializeRoute, serializeRoute } from "../lib/route";

interface RouteContextValue {
  route: Route;
  setRoute: (next: Partial<Route> | ((prev: Route) => Route)) => void;
  replaceRoute: (next: Route) => void;
}

const RouteContext = React.createContext<RouteContextValue | null>(null);

export function RouteProvider({ children }: { children: React.ReactNode }) {
  const [route, setRouteState] = React.useState<Route>(() => {
    if (typeof window === "undefined") return DEFAULT_ROUTE;
    const qs = window.location.search.slice(1);
    return qs ? deserializeRoute(qs) : DEFAULT_ROUTE;
  });

  // Sync to URL (shareable links) — replace, not push, so Back button
  // still feels sensible while inside the app.
  React.useEffect(() => {
    const qs = serializeRoute(route);
    const url = qs ? `?${qs}` : window.location.pathname;
    window.history.replaceState({}, "", url);
  }, [route]);

  const setRoute = React.useCallback((next: Partial<Route> | ((prev: Route) => Route)) => {
    setRouteState((prev) => {
      const merged = typeof next === "function" ? next(prev) : { ...prev, ...next };
      // When primary changes, clear irrelevant sub-ids
      if (merged.primary !== prev.primary) {
        if (merged.primary !== "agent") { merged.agentId = undefined; merged.agentTab = undefined; merged.sessionId = undefined; }
        if (merged.primary !== "workflow") { merged.workflowId = undefined; merged.workflowTab = undefined; }
        if (merged.primary !== "marketplace-item") merged.marketplaceId = undefined;
      }
      // Defaults
      if (merged.primary === "agent" && !merged.agentTab) merged.agentTab = "chat";
      if (merged.primary === "workflow" && !merged.workflowTab) merged.workflowTab = "overview";
      return merged;
    });
  }, []);

  const replaceRoute = React.useCallback((next: Route) => setRouteState(next), []);

  return (
    <RouteContext.Provider value={{ route, setRoute, replaceRoute }}>
      {children}
    </RouteContext.Provider>
  );
}

export function useRoute() {
  const ctx = React.useContext(RouteContext);
  if (!ctx) throw new Error("useRoute must be used inside <RouteProvider>");
  return ctx;
}
