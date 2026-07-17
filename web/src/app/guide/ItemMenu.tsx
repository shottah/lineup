"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { useMutation, useQuery } from "@tanstack/react-query";

import { api, ApiError } from "@/lib/api";
import type { Entry, GuideItem, GuideTitleLookup, ShelfResponse } from "@/lib/types";

import { GENERIC_ERROR, useGuideItemMutations } from "./useGuideItemMutations";

function pad2(n: number): string {
  return String(n).padStart(2, "0");
}

// "HH:MM" 24h, zero-padded — the native <input type="time"> format. Kept
// local to the Move picker: distinct from lib/guide's fmtTime, which
// renders a 12h display string ("8:00 pm") unsuitable for a time input.
function hhmm(startMin: number): string {
  const h = Math.floor(startMin / 60) % 24;
  const m = startMin % 60;
  return `${pad2(h)}:${pad2(m)}`;
}

function parseHHMM(value: string): number {
  const [h, m] = value.split(":").map(Number);
  return h * 60 + m;
}

function Chip({
  onClick,
  disabled,
  muted,
  pressed,
  children,
}: {
  onClick: () => void;
  disabled: boolean;
  muted?: boolean;
  pressed?: boolean;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      aria-pressed={pressed}
      onClick={onClick}
      className={`rounded-lg bg-panel2 px-2.5 py-[5px] text-[11px] font-medium whitespace-nowrap disabled:opacity-50 ${
        muted ? "text-mut" : "text-ink"
      }`}
    >
      {children}
    </button>
  );
}

export type ItemMenuProps = {
  guideId: number;
  item: GuideItem;
  title: GuideTitleLookup;
  // The column the card currently renders in — date/dow of item.date, not
  // necessarily the item's post-move location (the card unmounts/remounts
  // under its new column once a Move invalidates the guide).
  columnDate: string;
  columnDow: string;
  // Guide's date range as {date, dow} pairs, for the Move day <select> —
  // the same list CalendarView already computed via toCalendarColumns, so
  // ItemMenu doesn't re-derive date iteration itself.
  columns: { date: string; dow: string }[];
  onClose: () => void;
};

// Inline action row for one calendar slot card. Owns its own sub-picker
// state (Swap/Move expansion) and one useMutation per action; CalendarView
// owns whether this ItemMenu is mounted at all (the "one open at a time"
// rule lives there). onClose collapses the whole row back to the plain
// card — used once a Swap or Move completes, since those are multi-step
// interactions that should visibly resolve; Watched/Pin/Remove don't call
// it: Watched/Pin leave the row open for further actions, and Remove makes
// the card (and this menu) disappear on its own once the guide refetches.
export function ItemMenu({ guideId, item, title, columnDate, columnDow, columns, onClose }: ItemMenuProps) {
  const router = useRouter();

  const [swapOpen, setSwapOpen] = useState(false);
  const [moveOpen, setMoveOpen] = useState(false);
  const [moveDate, setMoveDate] = useState(columnDate);
  const [moveTime, setMoveTime] = useState(() => hhmm(item.start_min));

  const { show, invalidateGuide, itemPath, watchedM, pinM, removeM } = useGuideItemMutations({
    guideId,
    item,
    title,
    columnDow,
  });

  const swapM = useMutation({
    mutationFn: (entry: { title_id: number; name: string }) =>
      api(itemPath, { method: "PATCH", body: JSON.stringify({ title_id: entry.title_id }) }).then(
        () => entry,
      ),
    onError: (err) => {
      show(err instanceof ApiError && err.status === 422 ? "That title can't be swapped in." : GENERIC_ERROR);
    },
    onSuccess: (entry) => {
      show(`Swapped in ${entry.name}`);
      onClose();
    },
    onSettled: invalidateGuide,
  });

  const moveM = useMutation({
    mutationFn: () =>
      api(itemPath, {
        method: "PATCH",
        body: JSON.stringify({ date: moveDate, start_min: parseHHMM(moveTime) }),
      }),
    onError: () => show(GENERIC_ERROR),
    onSuccess: () => {
      show("Moved");
      onClose();
    },
    onSettled: invalidateGuide,
  });

  const busy =
    watchedM.isPending || pinM.isPending || swapM.isPending || moveM.isPending || removeM.isPending;

  // Rotation/watchlist only fetch once the picker is actually opened —
  // no point loading shelves for every closed ItemMenu on the grid.
  const rotationQuery = useQuery({
    queryKey: ["shelf", "rotation"],
    queryFn: () => api<ShelfResponse>("/v1/me/shelves/rotation"),
    enabled: swapOpen,
  });
  const watchlistQuery = useQuery({
    queryKey: ["shelf", "watchlist"],
    queryFn: () => api<ShelfResponse>("/v1/me/shelves/watchlist"),
    enabled: swapOpen,
  });

  const swapLoading = swapOpen && (rotationQuery.isPending || watchlistQuery.isPending);
  const swapCandidates: Entry[] = [];
  if (swapOpen && !swapLoading) {
    const seen = new Set<number>([item.title_id]);
    for (const entry of [...(rotationQuery.data?.entries ?? []), ...(watchlistQuery.data?.entries ?? [])]) {
      if (seen.has(entry.title_id)) continue;
      seen.add(entry.title_id);
      swapCandidates.push(entry);
    }
  }

  return (
    <div className="px-2.5 pb-2.5">
      <div className="flex flex-wrap gap-[5px]">
        <Chip disabled={busy} pressed={item.watched} onClick={() => watchedM.mutate()}>
          {item.watched ? "Unwatch" : "✓ Watched"}
        </Chip>
        <Chip disabled={busy} pressed={item.pinned} onClick={() => pinM.mutate()}>
          {item.pinned ? "Unpin" : "Pin"}
        </Chip>
        <Chip
          disabled={busy}
          pressed={swapOpen}
          onClick={() => {
            setSwapOpen((v) => !v);
            setMoveOpen(false);
          }}
        >
          Swap
        </Chip>
        <Chip
          disabled={busy}
          pressed={moveOpen}
          onClick={() => {
            setMoveOpen((v) => !v);
            setSwapOpen(false);
          }}
        >
          Move
        </Chip>
        <Chip disabled={busy} onClick={() => router.push(`/title/${title.kind}/${title.tmdb_id}`)}>
          Details
        </Chip>
        <Chip disabled={busy} muted onClick={() => removeM.mutate()}>
          Remove
        </Chip>
      </div>

      {swapOpen && (
        <div className="mt-[5px] flex flex-col gap-1 rounded-lg bg-panel2 p-1.5">
          {swapLoading ? (
            <p className="px-2 py-1 text-[11px] text-mut">Loading…</p>
          ) : swapCandidates.length === 0 ? (
            <p className="px-2 py-1 text-[11px] text-mut">Nothing to swap in yet.</p>
          ) : (
            swapCandidates.map((entry) => (
              <button
                key={entry.title_id}
                type="button"
                disabled={busy}
                onClick={() => swapM.mutate({ title_id: entry.title_id, name: entry.name })}
                className="rounded-md px-2 py-1 text-left text-[11.5px] font-medium text-ink hover:bg-panel disabled:opacity-50"
              >
                {entry.name}
              </button>
            ))
          )}
        </div>
      )}

      {moveOpen && (
        <div className="mt-[5px] flex flex-wrap items-center gap-1.5 rounded-lg bg-panel2 p-1.5">
          <select
            value={moveDate}
            onChange={(e) => setMoveDate(e.target.value)}
            disabled={busy}
            className="rounded-md border border-line bg-panel px-2 py-1 text-[11.5px] font-medium text-ink disabled:opacity-50"
          >
            {columns.map((c) => (
              <option key={c.date} value={c.date}>
                {c.dow}
              </option>
            ))}
          </select>
          <input
            type="time"
            value={moveTime}
            onChange={(e) => setMoveTime(e.target.value)}
            disabled={busy}
            className="rounded-md border border-line bg-panel px-2 py-1 text-[11.5px] font-medium text-ink disabled:opacity-50"
          />
          <Chip disabled={busy || !moveTime} onClick={() => moveM.mutate()}>
            Move
          </Chip>
        </div>
      )}
    </div>
  );
}
