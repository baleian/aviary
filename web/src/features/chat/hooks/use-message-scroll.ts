"use client";

import { useEffect, type RefObject, type DependencyList } from "react";

/**
 * useMessageScroll — auto-scrolls a container to the bottom whenever
 * any of the dependencies change.
 *
 * Future improvement: skip scroll when user has manually scrolled up
 * (stick-to-bottom UX). For now matches existing behavior.
 */
export function useMessageScroll(
  ref: RefObject<HTMLElement | null>,
  deps: DependencyList,
) {
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);
}
