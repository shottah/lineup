"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";

import { useToast } from "@/components/Providers";
import { Segmented, type SegmentedOption } from "@/components/Segmented";
import { StarRating } from "@/components/StarRating";
import { api, ApiError } from "@/lib/api";
import type { Entry, EntryStatus, Title } from "@/lib/types";

type EntryPatch = {
  status?: EntryStatus;
  rating?: number | null;
  favorite?: boolean;
};

const STATUS_OPTIONS: SegmentedOption<EntryStatus>[] = [
  { value: "watchlist", label: "Watchlist" },
  { value: "rotation", label: "Rotation" },
  { value: "watched", label: "Watched" },
];

// Shelf actions for one title. A null entry means "no relationship yet":
// status none, unrated, not favorite. Status is a Segmented radio with
// toggle-off (clicking the active option sets none). PATCHes the INTERNAL
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
      <div className="flex flex-wrap items-center gap-3.5">
        <Segmented
          options={STATUS_OPTIONS}
          value={status}
          onChange={(next) => mutation.mutate({ status: next === status ? "none" : next })}
          disabled={busy}
          ariaLabel="Shelf"
        />
        <button
          type="button"
          disabled={busy}
          aria-pressed={favorite}
          aria-label={favorite ? "Remove from favorites" : "Add to favorites"}
          onClick={() => mutation.mutate({ favorite: !favorite })}
          className={`flex h-[38px] w-[38px] items-center justify-center rounded-full border border-line bg-panel text-base disabled:opacity-50 ${
            favorite ? "text-acc" : "text-faint"
          }`}
        >
          ♥
        </button>
      </div>
      <StarRating value={rating} onRate={(v) => mutation.mutate({ rating: v })} disabled={busy} />
    </div>
  );
}
