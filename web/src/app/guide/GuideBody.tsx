"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { useToast } from "@/components/Providers";
import { Segmented, type SegmentedOption } from "@/components/Segmented";
import { api, ApiError } from "@/lib/api";
import { monthDay, toCalendarColumns } from "@/lib/guide";
import type { GuideResponse } from "@/lib/types";

import { BoardView } from "./BoardView";
import { CalendarView } from "./CalendarView";
import { GenerateBar } from "./GenerateBar";

type ViewMode = "calendar" | "board";

const VIEW_OPTIONS: SegmentedOption<ViewMode>[] = [
  { value: "calendar", label: "Calendar" },
  { value: "board", label: "Board" },
];

// Local (not UTC) calendar date, YYYY-MM-DD — "today" for the calendar's
// Tonight labeling and the board's default day.
function todayLocal(): string {
  const now = new Date();
  const y = now.getFullYear();
  const m = String(now.getMonth() + 1).padStart(2, "0");
  const d = String(now.getDate()).padStart(2, "0");
  return `${y}-${m}-${d}`;
}

// Header + view toggle + regenerate, shown once a guide exists. Split
// out from GuideBody so its hooks (view state, the regenerate mutation
// keyed off guide.id) only run once `data` is a real GuideResponse —
// no `data!`/`data?.id` needed.
function GuideView({ guide, today }: { guide: GuideResponse; today: string }) {
  const queryClient = useQueryClient();
  const { show } = useToast();
  const [view, setView] = useState<ViewMode>("calendar");

  const regenerate = useMutation({
    mutationFn: () =>
      api<GuideResponse>(`/v1/guides/${guide.id}/regenerate`, { method: "POST" }),
    onError: () => show("Couldn't save — try again."),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["guide"] });
      show("Re-planned your remaining evenings — watched and pinned stayed put");
    },
  });

  const columns = toCalendarColumns(guide, today);
  const evenings = columns.filter((c) => c.slots.length > 0).length;
  const off = columns.length - evenings;

  return (
    <main className="mx-auto max-w-[1280px] px-8">
      <div className="flex flex-wrap items-center justify-between gap-4 py-[26px]">
        <div className="flex flex-wrap items-baseline gap-3">
          <h1 className="text-[22px] font-semibold tracking-[-0.01em] text-ink">
            Week of {monthDay(guide.start_date)}
          </h1>
          <p className="text-[13px] text-mut">
            {evenings} evenings planned · {off} night{off === 1 ? "" : "s"} off
          </p>
        </div>
        <div className="flex items-center gap-2.5">
          <Segmented options={VIEW_OPTIONS} value={view} onChange={setView} ariaLabel="View" />
          <button
            type="button"
            disabled={regenerate.isPending}
            onClick={() => regenerate.mutate()}
            className="whitespace-nowrap rounded-full bg-acc-soft px-4 py-[7px] text-[12.5px] font-medium text-acc disabled:opacity-50"
          >
            ↻ Regenerate remaining
          </button>
        </div>
      </div>

      {view === "calendar" ? (
        <CalendarView guide={guide} today={today} />
      ) : (
        <BoardView guide={guide} today={today} />
      )}
    </main>
  );
}

export function GuideBody() {
  const today = todayLocal();
  const { data, error, isPending } = useQuery({
    queryKey: ["guide"],
    queryFn: () => api<GuideResponse>("/v1/guides/current"),
    // No guide yet (404) is a definitive answer — retrying just delays
    // GenerateBar showing up (title-page pattern).
    retry: (failureCount, err) =>
      !(err instanceof ApiError && err.status === 404) && failureCount < 2,
  });

  if (isPending) {
    return <p className="p-8 text-sm text-mut">Loading…</p>;
  }
  // Error states only when there is nothing to show: a background
  // refetch failure (e.g. after a mutation invalidates this query) keeps
  // rendering the cached guide rather than collapsing it mid-interaction.
  if (!data) {
    if (error instanceof ApiError && error.status === 404) {
      return (
        <main className="mx-auto max-w-[1280px] px-8">
          <GenerateBar />
        </main>
      );
    }
    return (
      <p className="p-8 text-sm text-mut">
        {"Can't load your guide right now — try again shortly."}
      </p>
    );
  }

  return <GuideView guide={data} today={today} />;
}
