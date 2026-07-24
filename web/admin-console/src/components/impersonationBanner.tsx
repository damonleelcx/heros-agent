"use client";

import { useTransition } from "react";
import { endImpersonation } from "@/lib/actions";

/**
 * EndImpersonationButton is the always-visible End control on the impersonation banner (FR25).
 *
 * It is a client component only so the click can fire the server action without a full navigation —
 * the ending itself happens on the server, where the session lives. It is deliberately the one
 * control on the banner that needs no confirmation: ending impersonation is the SAFE direction
 * (returning to your own scope), and friction on the exit is exactly backwards.
 */
export function EndImpersonationButton({ impersonationId }: { impersonationId: string }) {
  const [pending, start] = useTransition();
  return (
    <button
      type="button"
      className="danger"
      disabled={pending}
      onClick={() => start(() => endImpersonation(impersonationId))}
    >
      {pending ? "Ending…" : "End impersonation"}
    </button>
  );
}
