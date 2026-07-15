"use client";

import { useQuery } from "@tanstack/react-query";

import { AuthGate } from "@/components/AuthGate";
import { Nav } from "@/components/Nav";
import { api } from "@/lib/api";
import type { User } from "@/lib/types";

// Placeholder guide page: proves the authed API loop end-to-end. Issue #18
// replaces GuideBody; keep the AuthGate + Nav wrapper.
function GuideBody() {
  const { data, error, isPending } = useQuery({
    queryKey: ["me"],
    queryFn: () => api<User>("/v1/me"),
  });

  if (isPending) return <p className="p-8 text-sm text-zinc-500">Loading…</p>;
  if (error) return <p className="p-8 text-sm text-red-600">Could not load your profile.</p>;

  return (
    <div className="p-8">
      <h1 className="text-xl font-semibold text-zinc-950 dark:text-zinc-50">Guide coming soon</h1>
      <p className="mt-2 text-sm text-zinc-500">
        Signed in as {data.email} · region {data.region}
      </p>
    </div>
  );
}

export default function GuidePage() {
  return (
    <AuthGate>
      <Nav />
      <GuideBody />
    </AuthGate>
  );
}
