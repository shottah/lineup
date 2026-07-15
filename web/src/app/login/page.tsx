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
    <main className="flex min-h-[80vh] items-center justify-center py-10">
      <div className="w-[360px] rounded-[20px] border border-line bg-panel px-9 py-10 text-center">
        <div className="relative mx-auto mb-2.5 h-8 w-11 rounded-lg border-2 border-acc">
          <span className="absolute -top-3 left-[9px] h-[11px] w-0.5 -rotate-[28deg] bg-acc" />
          <span className="absolute -top-3 right-[9px] h-[11px] w-0.5 rotate-[28deg] bg-acc" />
        </div>
        <h1 className="text-[26px] font-semibold tracking-[-0.01em] text-ink">Lineup</h1>
        <p className="mt-2 text-sm text-mut">Your week of TV, planned like a lineup.</p>
        <button
          disabled={busy}
          onClick={async () => {
            // busy stays true on success: the effect below navigates once the auth context updates, and re-enabling would invite double-submits
            setBusy(true);
            setError(null);
            try {
              await signInWithGoogle();
            } catch {
              setError("Sign-in failed. Try again.");
              setBusy(false);
            }
          }}
          className="mt-[22px] flex w-full items-center justify-center gap-2.5 rounded-full bg-ink px-5 py-3 text-sm font-semibold text-bg disabled:opacity-50"
        >
          <span className="flex h-5 w-5 items-center justify-center rounded-full bg-bg text-xs font-bold text-ink">
            G
          </span>
          Continue with Google
        </button>
        {config.appleAuth && (
          <button className="mt-3 flex w-full items-center justify-center gap-2.5 rounded-full border border-line px-5 py-3 text-sm font-semibold text-ink">
            Continue with Apple
          </button>
        )}
        {error && <p className="mt-3 text-sm text-danger">{error}</p>}
        <p className="mt-3.5 text-[11px] text-faint">
          One evening at a time. No autoplay, no feeds.
        </p>
      </div>
    </main>
  );
}
