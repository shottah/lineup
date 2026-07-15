"use client";

import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import { useToast } from "@/components/Providers";
import { api, ApiError } from "@/lib/api";
import type { GuideResponse } from "@/lib/types";

const MIN_DAYS = 1;
const MAX_DAYS = 14;
const DEFAULT_DAYS = 7;

// Local (not UTC) calendar date, YYYY-MM-DD — matches what a native
// <input type="date"> shows and what "today" means to the person
// planning their week, not what UTC says it is.
function todayLocal(): string {
  const now = new Date();
  const y = now.getFullYear();
  const m = String(now.getMonth() + 1).padStart(2, "0");
  const d = String(now.getDate()).padStart(2, "0");
  return `${y}-${m}-${d}`;
}

// No-guide-yet state (404 on GET /v1/guides/current): a centered panel
// form to generate the first week. GuideBody renders this in place of
// the header/view toggle, which only apply once a guide exists.
export function GenerateBar() {
  const queryClient = useQueryClient();
  const { show } = useToast();
  const [startDate, setStartDate] = useState(todayLocal);
  const [days, setDays] = useState(DEFAULT_DAYS);

  const mutation = useMutation({
    mutationFn: () =>
      api<GuideResponse>("/v1/guides", {
        method: "POST",
        body: JSON.stringify({ start_date: startDate, days }),
      }),
    onError: (err) => {
      if (err instanceof ApiError && err.status === 422) {
        show("Couldn't generate — check the dates.");
      } else {
        show("Couldn't save — try again.");
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["guide"] });
    },
  });

  const busy = mutation.isPending;

  return (
    <div className="flex justify-center py-16">
      <div className="w-[360px] rounded-[20px] border border-line bg-panel px-9 py-10 text-center">
        <h1 className="text-[22px] font-semibold tracking-[-0.01em] text-ink">Plan your week</h1>
        <div className="mt-6 flex flex-col gap-4 text-left">
          <label className="flex flex-col gap-1.5 text-[12.5px] font-medium text-mut">
            Start date
            <input
              type="date"
              value={startDate}
              onChange={(e) => setStartDate(e.target.value)}
              className="rounded-lg border border-line bg-panel2 px-3 py-2 text-[13px] font-medium text-ink"
            />
          </label>
          <label className="flex flex-col gap-1.5 text-[12.5px] font-medium text-mut">
            Days
            <input
              type="number"
              min={MIN_DAYS}
              max={MAX_DAYS}
              value={days}
              onChange={(e) => setDays(Number(e.target.value))}
              className="rounded-lg border border-line bg-panel2 px-3 py-2 text-[13px] font-medium text-ink"
            />
          </label>
        </div>
        <button
          type="button"
          disabled={busy}
          onClick={() => mutation.mutate()}
          className="mt-7 w-full rounded-full bg-acc px-5 py-3 text-sm font-semibold text-acc-ink disabled:opacity-50"
        >
          Generate
        </button>
      </div>
    </div>
  );
}
