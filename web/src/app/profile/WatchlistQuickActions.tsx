"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";

import { useToast } from "@/components/Providers";
import { api, ApiError } from "@/lib/api";
import type { Entry, EntryStatus } from "@/lib/types";

type EntryPatch = { status?: EntryStatus; favorite?: boolean };

// Hover/keyboard quick actions for a watchlist card: favorite toggle,
// add to rotation, remove from watchlist. Mirrors EntryActions' mutation
// semantics. Hover-only by design (recorded v1 limitation) — touch flows
// use the title page's full action set.
export function WatchlistQuickActions({ entry }: { entry: Entry }) {
  const queryClient = useQueryClient();
  const { show } = useToast();

  const mutation = useMutation({
    mutationFn: (patch: EntryPatch) =>
      api<Entry>(`/v1/titles/${entry.title_id}/entry`, {
        method: "PATCH",
        body: JSON.stringify(patch),
      }),
    onSuccess: (_data, patch) => {
      if (patch.status === "rotation") {
        show("Added to rotation");
      }
      if (patch.status === "none") {
        show("Removed from watchlist");
      }
    },
    onError: (err) => {
      if (err instanceof ApiError && err.status === 409 && err.code === "rotation_full") {
        show("Rotation is full (10); finish something first.");
      } else {
        show("Couldn't save — try again.");
      }
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ["shelf"] });
      queryClient.invalidateQueries({
        queryKey: ["title", entry.kind, String(entry.tmdb_id)],
      });
    },
  });

  const busy = mutation.isPending;

  return (
    <div className="pointer-events-none absolute inset-x-2 bottom-2 z-10 flex justify-center gap-1.5 opacity-0 transition-opacity group-focus-within:pointer-events-auto group-focus-within:opacity-100 group-hover:pointer-events-auto group-hover:opacity-100">
      <button
        type="button"
        disabled={busy}
        aria-pressed={entry.favorite}
        aria-label={entry.favorite ? "Remove from favorites" : "Add to favorites"}
        onClick={() => mutation.mutate({ favorite: !entry.favorite })}
        className={`rounded-full border border-line bg-panel/90 px-2.5 py-1.5 text-sm backdrop-blur disabled:opacity-50 ${
          entry.favorite ? "text-acc" : "text-faint"
        }`}
      >
        ♥
      </button>
      <button
        type="button"
        disabled={busy}
        aria-label="Add to rotation"
        onClick={() => mutation.mutate({ status: "rotation" })}
        className="rounded-full border border-line bg-panel/90 px-2.5 py-1.5 text-xs font-medium text-ink backdrop-blur disabled:opacity-50"
      >
        ＋ Rotation
      </button>
      <button
        type="button"
        disabled={busy}
        aria-label="Remove from watchlist"
        onClick={() => mutation.mutate({ status: "none" })}
        className="rounded-full border border-line bg-panel/90 px-2.5 py-1.5 text-xs font-medium text-mut backdrop-blur disabled:opacity-50"
      >
        ✕
      </button>
    </div>
  );
}
