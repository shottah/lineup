"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";

import { useAuth } from "@/components/Providers";
import { config } from "@/lib/config";
import { signInWithGoogle } from "@/lib/firebase";

export default function LoginPage() {
  const { user, loading } = useAuth();
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!loading && user) {
      router.replace("/guide");
    }
  }, [loading, user, router]);

  return (
    <main className="flex flex-1 items-center justify-center p-8">
      <div className="w-full max-w-sm rounded-xl border border-zinc-200 p-8 text-center dark:border-zinc-800">
        <h1 className="text-2xl font-semibold text-zinc-950 dark:text-zinc-50">Lineup</h1>
        <p className="mt-2 text-sm text-zinc-500">Your week of TV, planned like a lineup</p>
        <button
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            setError(null);
            try {
              await signInWithGoogle();
              router.replace("/guide");
            } catch {
              setError("Sign-in failed. Try again.");
              setBusy(false);
            }
          }}
          className="mt-6 w-full rounded-lg bg-zinc-950 px-4 py-2 text-zinc-50 disabled:opacity-50 dark:bg-zinc-50 dark:text-zinc-950"
        >
          Continue with Google
        </button>
        {config.appleAuth && (
          <button className="mt-3 w-full rounded-lg border border-zinc-300 px-4 py-2 text-zinc-950 dark:border-zinc-700 dark:text-zinc-50">
            Continue with Apple
          </button>
        )}
        {error && <p className="mt-3 text-sm text-red-600">{error}</p>}
      </div>
    </main>
  );
}
