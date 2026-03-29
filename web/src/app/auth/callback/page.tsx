"use client";

import { Suspense, useEffect, useRef, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { handleCallback } from "@/lib/auth";
import { useAuth } from "@/components/providers/auth-provider";

function CallbackHandler() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const { refreshUser } = useAuth();
  const [error, setError] = useState<string | null>(null);
  const processed = useRef(false);

  useEffect(() => {
    // Prevent double-execution in React Strict Mode
    if (processed.current) return;

    const code = searchParams.get("code");
    const state = searchParams.get("state");
    const errorParam = searchParams.get("error");

    if (errorParam) {
      setError(searchParams.get("error_description") || errorParam);
      return;
    }

    if (!code || !state) {
      setError("Missing authorization code or state");
      return;
    }

    processed.current = true;

    handleCallback(code, state)
      .then(async () => {
        // Update AuthProvider state before navigating
        await refreshUser();
        router.replace("/agents");
      })
      .catch((err) => {
        setError(err.message);
        processed.current = false;
      });
  }, [searchParams, router, refreshUser]);

  if (error) {
    return (
      <div className="flex min-h-screen flex-col items-center justify-center gap-4">
        <h2 className="text-xl font-semibold text-destructive">
          Authentication Failed
        </h2>
        <p className="text-muted-foreground">{error}</p>
        <a href="/login" className="text-primary underline">
          Back to login
        </a>
      </div>
    );
  }

  return (
    <div className="flex min-h-screen items-center justify-center">
      <div className="text-muted-foreground">Completing sign in...</div>
    </div>
  );
}

export default function AuthCallbackPage() {
  return (
    <Suspense
      fallback={
        <div className="flex min-h-screen items-center justify-center">
          <div className="text-muted-foreground">Loading...</div>
        </div>
      }
    >
      <CallbackHandler />
    </Suspense>
  );
}
