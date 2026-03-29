"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/components/providers/auth-provider";
import { Button } from "@/components/ui/button";

export default function LoginPage() {
  const { user, isLoading, login } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (!isLoading && user) {
      router.replace("/agents");
    }
  }, [user, isLoading, router]);

  return (
    <div className="relative flex min-h-screen flex-col items-center justify-center overflow-hidden">
      {/* Background gradient orbs */}
      <div className="pointer-events-none absolute inset-0">
        <div className="absolute left-1/4 top-1/4 h-96 w-96 rounded-full bg-primary/5 blur-3xl" />
        <div className="absolute bottom-1/4 right-1/4 h-80 w-80 rounded-full bg-primary/3 blur-3xl" />
      </div>

      <div className="relative z-10 flex flex-col items-center gap-10 animate-fade-in">
        {/* Logo & Brand */}
        <div className="flex flex-col items-center gap-4">
          <div className="flex h-16 w-16 items-center justify-center rounded-2xl bg-primary/10 glow-primary">
            <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" className="text-primary">
              <path d="M12 2L2 7l10 5 10-5-10-5z" />
              <path d="M2 17l10 5 10-5" />
              <path d="M2 12l10 5 10-5" />
            </svg>
          </div>
          <div className="flex flex-col items-center gap-2">
            <h1 className="text-4xl font-bold tracking-tight text-foreground">
              Aviary
            </h1>
            <p className="text-base text-muted-foreground">
              Deploy, manage, and chat with AI agents
            </p>
          </div>
        </div>

        {/* Sign in card */}
        <div className="w-full max-w-sm rounded-2xl border border-border/50 bg-card/50 p-8 backdrop-blur-sm">
          <Button
            size="lg"
            onClick={login}
            disabled={isLoading}
            className="w-full glow-primary-sm"
          >
            {isLoading ? (
              <span className="flex items-center gap-2">
                <svg className="h-4 w-4 animate-spin" viewBox="0 0 24 24" fill="none">
                  <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                  <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                </svg>
                Connecting...
              </span>
            ) : (
              "Sign in with SSO"
            )}
          </Button>
          <p className="mt-4 text-center text-xs text-muted-foreground">
            Authenticate via your organization&apos;s identity provider
          </p>
        </div>
      </div>
    </div>
  );
}
