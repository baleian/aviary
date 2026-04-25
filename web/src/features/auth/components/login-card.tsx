"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/features/auth/providers/auth-provider";
import { fetchAuthConfig } from "@/lib/auth";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { AviaryLogo } from "@/components/brand/aviary-logo";
import { routes } from "@/lib/constants/routes";

export function LoginCard() {
  const { user, status, login } = useAuth();
  const router = useRouter();
  const [idpEnabled, setIdpEnabled] = useState<boolean | null>(null);

  useEffect(() => {
    if (status === "authenticated" && user) {
      router.replace(routes.home);
    }
  }, [status, user, router]);

  useEffect(() => {
    let cancelled = false;
    fetchAuthConfig()
      .then((cfg) => {
        if (cancelled) return;
        setIdpEnabled(cfg.idp_enabled);
        if (!cfg.idp_enabled && status === "unauthenticated") {
          void login();
        }
      })
      .catch(() => setIdpEnabled(true));
    return () => {
      cancelled = true;
    };
  }, [status, login]);

  const isLoading = status === "loading" || idpEnabled === null;
  const buttonLabel = idpEnabled === false ? "Continuing as Dev User" : "Sign in with SSO";
  const helperCopy =
    idpEnabled === false
      ? "No identity provider configured — running as the local dev user"
      : "Authenticate via your organization's identity provider";

  return (
    <div className="relative flex min-h-screen flex-col items-center justify-center px-6 overflow-hidden">
      <div className="relative flex flex-col items-center gap-10 animate-fade-in">
        <div className="flex flex-col items-center gap-5">
          <div className="relative">
            <AviaryLogo size={104} />
          </div>
          <div className="flex flex-col items-center gap-2">
            <h1 className="type-display text-fg-primary">Aviary</h1>
            <p className="type-caption text-fg-muted tracking-[0.18em] uppercase">
              AI Agent Platform
            </p>
          </div>
        </div>

        <div className="w-full max-w-sm rounded-xl">
          <div className="bg-raised border border-border-subtle rounded-xl shadow-xl p-8">
            <Button
              variant="cta"
              size="lg"
              onClick={login}
              disabled={isLoading || idpEnabled === false}
              className="w-full"
            >
              {isLoading || idpEnabled === false ? (
                <>
                  <Spinner size={16} className="text-white" />
                  {idpEnabled === false ? buttonLabel : "Connecting…"}
                </>
              ) : (
                buttonLabel
              )}
            </Button>
            <p className="mt-5 text-center type-caption text-fg-muted">{helperCopy}</p>
          </div>
        </div>
      </div>
    </div>
  );
}
