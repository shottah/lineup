"use client";

import { useState } from "react";

import { toCalendarColumns } from "@/lib/guide";
import type { GuideResponse } from "@/lib/types";

import { ItemMenu } from "./ItemMenu";

// The 7-column calendar: desktop grid, below-lg a horizontal snap-scroll
// row. Owns the "one ItemMenu open at a time" state — keyed by
// `${date}-${itemId}` so the same item id on two different columns (can't
// happen today, but the key stays date-qualified for safety) never
// collides.
export function CalendarView({ guide, today }: { guide: GuideResponse; today: string }) {
  const [openKey, setOpenKey] = useState<string | null>(null);
  const columns = toCalendarColumns(guide, today);
  const dateOptions = columns.map((c) => ({ date: c.date, dow: c.dow }));

  return (
    <div className="flex gap-2 overflow-x-auto pb-2 snap-x lg:grid lg:grid-cols-7">
      {columns.map((col) => (
        <div key={col.date} className="flex min-w-[160px] flex-col gap-1.5 snap-start">
          <div className="px-1 pt-1 pb-2 text-center">
            <div
              className={`text-[11px] font-semibold tracking-[0.1em] ${
                col.isToday ? "text-acc" : "text-ink"
              }`}
            >
              {col.dow}
            </div>
            <div className={`text-[10.5px] ${col.isToday ? "text-acc" : "text-faint"}`}>{col.sub}</div>
          </div>

          {col.slots.map((slot) => {
            const key = `${col.date}-${slot.item.id}`;
            const open = openKey === key;
            return (
              <div key={key} className={`rounded-xl bg-panel ${slot.item.watched ? "opacity-50" : ""}`}>
                <button
                  type="button"
                  onClick={() => setOpenKey(open ? null : key)}
                  className="block w-full rounded-xl px-3 pt-[11px] pb-3 text-left text-ink"
                >
                  <div className="text-[10px] font-medium text-mut">{slot.timeLabel}</div>
                  <div className="mt-[3px] text-[13.5px] leading-[1.25] font-semibold">
                    {slot.item.watched ? "✓ " : ""}
                    {slot.title.name}
                  </div>
                  <div className="mt-[3px] text-[10.5px] text-mut">{slot.sub}</div>
                  {slot.item.pinned && (
                    <span className="mt-1.5 inline-block rounded-full bg-acc-soft px-2 py-0.5 text-[9.5px] font-medium text-acc">
                      Pinned
                    </span>
                  )}
                </button>
                {open && (
                  <ItemMenu
                    guideId={guide.id}
                    item={slot.item}
                    title={slot.title}
                    columnDate={col.date}
                    columnDow={col.dow}
                    columns={dateOptions}
                    onClose={() => setOpenKey(null)}
                  />
                )}
              </div>
            );
          })}

          {col.slots.length === 0 && (
            <div className="rounded-xl border border-dashed border-line px-2.5 py-5 text-center text-[11.5px] font-medium text-faint">
              Night off
            </div>
          )}
        </div>
      ))}
    </div>
  );
}
