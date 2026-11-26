"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

import { useAuth } from "@/components/Providers";
import { TimezoneSync } from "@/components/TimezoneSync";

// Renders children only for signed-in users; bounces others to /login.
// Renders nothing while auth state is still resolving to avoid a flash of
// protected content. Signed-in renders also sync the browser timezone
// onto the user (TimezoneSync).
export function AuthGate({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (!loading && !user) {
      router.replace("/login");
    }
  }, [loading, user, router]);

  if (loading || !user) {
    return null;
  }
  return (
    <>
      <TimezoneSync />
      {children}
    </>
  );
}
