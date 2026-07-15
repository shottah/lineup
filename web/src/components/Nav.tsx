"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useQueryClient } from "@tanstack/react-query";

import { useAuth } from "@/components/Providers";
import { signOutUser } from "@/lib/firebase";

export function Nav() {
  const { user } = useAuth();
  const router = useRouter();
  const queryClient = useQueryClient();

  return (
    <nav className="flex items-center justify-between border-b border-zinc-200 px-6 py-3 dark:border-zinc-800">
      <div className="flex items-center gap-4">
        <Link href="/guide" className="font-semibold text-zinc-950 dark:text-zinc-50">
          Lineup
        </Link>
        <Link
          href="/search"
          className="text-sm text-zinc-500 hover:text-zinc-950 dark:hover:text-zinc-50"
        >
          Search
        </Link>
      </div>
      <div className="flex items-center gap-4 text-sm">
        <span className="text-zinc-500">{user?.email}</span>
        <button
          onClick={async () => {
            await signOutUser();
            // Drop all cached query data so the next account never sees the
            // previous account's profile served from cache.
            queryClient.clear();
            router.replace("/login");
          }}
          className="text-zinc-950 underline underline-offset-4 dark:text-zinc-50"
        >
          Sign out
        </button>
      </div>
    </nav>
  );
}
