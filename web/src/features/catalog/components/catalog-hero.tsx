"use client";

import { Sparkles } from "@/components/icons";

export function CatalogHero({ total }: { total: number | null }) {
  return (
    <section className="relative flex flex-col items-center pt-14 pb-10 text-center animate-fade-in">
      <div
        aria-hidden
        className="pointer-events-none absolute inset-x-0 top-0 mx-auto h-40 max-w-2xl rounded-full bg-aurora-c opacity-[0.18] blur-3xl"
      />

      <div className="relative inline-flex items-center gap-2 rounded-pill glass-raised px-3 py-1 type-caption text-fg-muted">
        <Sparkles size={12} strokeWidth={1.75} className="text-aurora-coral" />
        <span>Discover</span>
      </div>

      <h1 className="relative mt-6 max-w-2xl text-balance type-display-hero tracking-tight">
        <span className="text-aurora-c">Agents</span>{" "}
        built by your team.
      </h1>

      <p className="relative mt-4 max-w-md text-balance type-body-lg text-fg-muted">
        Browse published agents, pick versions, and import them into your own workspace.
      </p>

      {total !== null && (
        <p className="relative mt-6 type-caption text-fg-disabled">
          {total} {total === 1 ? "agent" : "agents"} published
        </p>
      )}
    </section>
  );
}
