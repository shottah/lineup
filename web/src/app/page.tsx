"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

import { useAuth } from "@/components/Providers";

// Root route: pure dispatcher on auth state. The marketing landing returns
// with the launch pass (issue #19).
export default function Home() {
  const { user, loading } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (loading) return;
    router.replace(user ? "/guide" : "/login");
  }, [user, loading, router]);

  return null;
}
