"use client";

import { Fragment, useState, type CSSProperties } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import { useToast } from "@/components/Providers";
import { api, ApiError } from "@/lib/api";
import { toBoardRows, toCalendarColumns, type BoardCell } from "@/lib/guide";
import type { GuideResponse } from "@/lib/types";

const GENERIC_ERROR = "Couldn't save — try again.";

// toCalendarColumns' dow ("MON") title-cased for the day chips ("Mon")
// and mapped to a full weekday name for the heading ("Monday evening") —
// reuses the mapper's day-of-week derivation rather than touching Date
// again here.
const FULL_DOW: Record<string, string> = {
  SUN: "Sunday",
  MON: "Monday",
  TUE: "Tuesday",
  WED: "Wednesday",
  THU: "Thursday",
  FRI: "Friday",
  SAT: "Saturday",
};

function titleCase(dow: string): string {
  return dow.charAt(0) + dow.slice(1).toLowerCase();
}

type SwapVars = { targetId: number; titleId: number; titleName: string };

// The TV-guide board: day chips over a provider x hour grid for one
// selected date (toBoardRows(guide, date)). Plan cells (the evening's
// numbered picks) are inert; an alternate cell swaps into its hour's plan
// item on click, but only when the mapper attached a swapTargetId (a plan
// item sharing that timeslot) — an alternate with no plan neighbor in its
// hour renders but does not respond to clicks.
export function BoardView({ guide, today }: { guide: GuideResponse; today: string }) {
  const queryClient = useQueryClient();
  const { show } = useToast();

  // toCalendarColumns already computes one entry per guide date with a
  // dow label; the board reuses it for chip iteration instead of
  // re-deriving the date range.
  const columns = toCalendarColumns(guide, today);
  const [selectedDate, setSelectedDate] = useState(() =>
    columns.some((c) => c.date === today) ? today : columns[0].date,
  );

  const swapM = useMutation({
    mutationFn: (vars: SwapVars) =>
      api(`/v1/guides/${guide.id}/items/${vars.targetId}`, {
        method: "PATCH",
        body: JSON.stringify({ title_id: vars.titleId }),
      }),
    onError: (err) => {
      show(err instanceof ApiError && err.status === 422 ? "That title can't be swapped in." : GENERIC_ERROR);
    },
    onSuccess: (_data, vars) => show(`Swapped in ${vars.titleName}`),
    onSettled: () => queryClient.invalidateQueries({ queryKey: ["guide"] }),
  });

  const selectedColumn = columns.find((c) => c.date === selectedDate) ?? columns[0];
  const board = toBoardRows(guide, selectedDate);
  const emptyDay = board.rows.length === 0;

  return (
    <div>
      <div className="flex flex-wrap gap-1 pb-4">
        {columns.map((col) => {
          const active = col.date === selectedDate;
          return (
            <button
              key={col.date}
              type="button"
              aria-pressed={active}
              onClick={() => setSelectedDate(col.date)}
              className={`rounded-full px-3.5 py-1.5 text-[12px] font-semibold ${
                active ? "bg-ink text-bg" : "text-mut"
              }`}
            >
              {titleCase(col.dow)}
            </button>
          );
        })}
      </div>

      <div className="flex flex-wrap items-baseline gap-3 pb-3">
        <h2 className="text-[16px] font-semibold text-ink">{FULL_DOW[selectedColumn.dow]} evening</h2>
        <p className="text-[12.5px] text-mut">
          {emptyDay ? "Night off — nothing planned" : `Your path: ${board.path.join(" → ")}`}
        </p>
      </div>

      {!emptyDay && (
        <>
          <div className="overflow-x-auto pb-3.5">
            <div
              className="grid gap-1.5 [grid-template-columns:120px_repeat(var(--board-cols),1fr)]"
              style={{ "--board-cols": board.times.length } as CSSProperties}
            >
              <div />
              {board.times.map((t) => (
                <div key={t.startMin} className="px-1 text-[10.5px] font-medium text-mut">
                  {t.label}
                </div>
              ))}
              {board.rows.map((row) => (
                <Fragment key={row.providerId}>
                  <div className="self-center text-[12px] font-semibold text-ink">{row.providerName}</div>
                  {row.cells.map((cell, i) => (
                    <div key={board.times[i]?.startMin ?? i} className="min-h-[58px]">
                      {cell.has && (
                        <BoardCellView cell={cell} swapPending={swapM.isPending} onSwap={swapM.mutate} />
                      )}
                    </div>
                  ))}
                </Fragment>
              ))}
            </div>
          </div>
          <p className="pb-7 text-[11.5px] text-faint">
            Numbered cards are your evening · tap any alternate to swap it in
          </p>
        </>
      )}
    </div>
  );
}

function BoardCellView({
  cell,
  swapPending,
  onSwap,
}: {
  cell: Extract<BoardCell, { has: true }>;
  swapPending: boolean;
  onSwap: (vars: SwapVars) => void;
}) {
  // Plan cells are the evening's picks — display only, never clickable.
  if (cell.item.is_plan) {
    return (
      <div className="relative h-full rounded-xl border border-acc bg-panel px-3 py-2.5">
        {cell.step !== null && (
          <div className="absolute -top-2 left-3 h-[17px] w-[17px] rounded-full bg-acc text-center text-[10px] font-bold leading-[17px] text-acc-ink">
            {cell.step}
          </div>
        )}
        <div className="text-[13px] font-semibold text-ink">{cell.title.name}</div>
        <div className="mt-0.5 text-[10.5px] text-acc">{cell.sub}</div>
      </div>
    );
  }

  const body = (
    <>
      <div className="text-[13px] font-semibold text-mut">{cell.title.name}</div>
      <div className="mt-0.5 text-[10.5px] text-faint">{cell.sub}</div>
    </>
  );

  // No plan item shares this alternate's timeslot — the mapper leaves
  // swapTargetId unset, and the cell stays inert (no button, no handler).
  if (cell.swapTargetId === undefined) {
    return <div className="h-full rounded-xl bg-panel2 px-3 py-2.5">{body}</div>;
  }

  const targetId = cell.swapTargetId;
  return (
    <button
      type="button"
      disabled={swapPending}
      onClick={() => onSwap({ targetId, titleId: cell.item.title_id, titleName: cell.title.name })}
      className="block h-full w-full rounded-xl bg-panel2 px-3 py-2.5 text-left disabled:opacity-50"
    >
      {body}
    </button>
  );
}
