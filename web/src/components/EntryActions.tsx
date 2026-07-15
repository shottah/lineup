"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";

import { useToast } from "@/components/Providers";
import { StarRating } from "@/components/StarRating";
import { api, ApiError } from "@/lib/api";
import type { Entry, EntryStatus, Title } from "@/lib/types";

type EntryPatch = {
  status?: EntryStatus;
  rating?: number | null;
  favorite?: boolean;
};

const STATUSES: { value: Exclude<EntryStatus, "none">; label: string }[] = [
  { value: "watchlist", label: "Watchlist" },
  { value: "rotation", label: "Rotation" },
  { value: "watched", label: "Watched" },
];

// Shelf actions for one title. A null entry means "no relationship yet":
// status none, unrated, not favorite. Status buttons are a radio with
// toggle-off (clicking the active one sets none). PATCHes the INTERNAL
// title id — never the TMDB id.
export function EntryActions({
  title,
  entry,
  kind,
  tmdbId,
}: {
  title: Title;
  entry: Entry | null;
  kind: string;
  tmdbId: string;
}) {
  const queryClient = useQueryClient();
  const { show } = useToast();

  const status: EntryStatus = entry?.status ?? "none";
  const rating = entry?.rating ?? null;
  const favorite = entry?.favorite ?? false;

  const mutation = useMutation({
    mutationFn: (patch: EntryPatch) =>
      api<Entry>(`/v1/titles/${title.id}/entry`, {
        method: "PATCH",
        body: JSON.stringify(patch),
      }),
    onError: (err) => {
      if (err instanceof ApiError && err.status === 409 && err.code === "rotation_full") {
        show("Rotation is full (8); finish something first.");
      } else {
        show("Couldn't save — try again.");
      }
    },
    onSettled: () => {
      // Refetch whether it worked or not: on error the server state is
      // unknown, and the title payload is the source of truth.
      queryClient.invalidateQueries({ queryKey: ["title", kind, tmdbId] });
      queryClient.invalidateQueries({ queryKey: ["shelf"] });
    },
  });

  const busy = mutation.isPending;

  return (
    <div className="mt-6 flex flex-col gap-4">
      <div className="flex items-center gap-2">
        {STATUSES.map((s) => {
          const active = status === s.value;
          return (
            <button
              key={s.value}
              type="button"
              disabled={busy}
              aria-pressed={active}
              onClick={() => mutation.mutate({ status: active ? "none" : s.value })}
              className={`rounded-lg border px-3 py-1.5 text-sm disabled:opacity-50 ${
                active
                  ? "border-zinc-950 bg-zinc-950 text-zinc-50 dark:border-zinc-50 dark:bg-zinc-50 dark:text-zinc-950"
                  : "border-zinc-300 text-zinc-950 dark:border-zinc-700 dark:text-zinc-50"
              }`}
            >
              {s.label}
            </button>
          );
        })}
        <button
          type="button"
          disabled={busy}
          aria-pressed={favorite}
          aria-label={favorite ? "Remove from favorites" : "Add to favorites"}
          onClick={() => mutation.mutate({ favorite: !favorite })}
          className={`ml-2 text-xl disabled:opacity-50 ${
            favorite ? "text-red-500" : "text-zinc-300 dark:text-zinc-700"
          }`}
        >
          ♥
        </button>
      </div>
      <StarRating value={rating} onRate={(v) => mutation.mutate({ rating: v })} disabled={busy} />
    </div>
  );
}
