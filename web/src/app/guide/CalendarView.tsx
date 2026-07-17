"use client";

import { useState, type CSSProperties } from "react";

import { toCalendarColumns, type CalendarSlot } from "@/lib/guide";
import type { GuideResponse } from "@/lib/types";

import { epLabel } from "./epLabel";
import { ItemMenu } from "./ItemMenu";
import { ProviderChip } from "./ProviderChip";
import { usePosterHue } from "./usePosterHue";

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
    <div className="flex gap-2 overflow-x-auto pb-2 snap-x lg:overflow-visible lg:grid lg:grid-cols-7">
      {columns.map((col) => (
        <div key={col.date} className="flex min-w-[160px] lg:min-w-0 flex-col gap-1.5 snap-start">
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
            return (
              <CalendarSlotCard
                key={key}
                guide={guide}
                slot={slot}
                columnDate={col.date}
                columnDow={col.dow}
                dateOptions={dateOptions}
                open={openKey === key}
                onToggleOpen={() => setOpenKey(openKey === key ? null : key)}
                onClose={() => setOpenKey(null)}
              />
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

// Poster-tinted card (docs/design/guide-card-redesign.md §1–§4): a
// dedicated component (not inlined in the .map() above) because
// usePosterHue is a hook and each slot needs its own independent hook
// call — inlining it in the loop body would call hooks a variable number
// of times per render, which breaks the Rules of Hooks.
function CalendarSlotCard({
  guide,
  slot,
  columnDate,
  columnDow,
  dateOptions,
  open,
  onToggleOpen,
  onClose,
}: {
  guide: GuideResponse;
  slot: CalendarSlot;
  columnDate: string;
  columnDow: string;
  dateOptions: { date: string; dow: string }[];
  open: boolean;
  onToggleOpen: () => void;
  onClose: () => void;
}) {
  const hue = usePosterHue(slot.item.title_id, slot.title.poster_path);
  const watched = slot.item.watched;
  const provider = guide.providers[String(slot.item.provider_id)];
  const [timeNumber, meridiem] = slot.timeLabel.split(" ");

  return (
    <div
      className={`guide-card group relative rounded-xl border-[hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.55)] border bg-[color-mix(in_srgb,hsl(var(--th)_var(--tint-s)_var(--tint-l))_7%,var(--color-panel))] transition-[box-shadow,transform] duration-200 ease-out hover:-translate-y-px focus-within:-translate-y-px hover:shadow-[0_0_0_1px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.45),0_6px_18px_-6px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.4)] focus-within:shadow-[0_0_0_1px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.45),0_6px_18px_-6px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.4)] ${watched ? "opacity-50" : ""}`}
      style={{ "--th": hue } as CSSProperties}
    >
      <button
        type="button"
        onClick={onToggleOpen}
        className="block w-full rounded-xl px-3 pt-[11px] pb-3 text-left text-ink"
      >
        <div className="mt-0 inline-flex items-center gap-[3px] rounded-full border border-line bg-panel2/70 px-2 py-[3px]">
          <span className="text-[10px] font-semibold tabular-nums text-mut">{timeNumber}</span>
          <span className="text-[8.5px] font-medium text-faint">{meridiem}</span>
        </div>
        <div className="mt-[3px] text-[13.5px] leading-[1.25] font-semibold">
          {watched ? "✓ " : ""}
          {slot.title.name}
        </div>
        <div className="mt-[3px] flex items-center gap-1 text-[10.5px] text-mut">
          <span>{epLabel(slot.title, slot.item)}</span>
          {slot.providerName && (
            <>
              <span aria-hidden="true">·</span>
              <ProviderChip
                variant="inline"
                providerId={slot.item.provider_id}
                logoPath={provider?.logo_path ?? ""}
                providerName={slot.providerName}
              />
            </>
          )}
        </div>
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
          columnDate={columnDate}
          columnDow={columnDow}
          columns={dateOptions}
          onClose={onClose}
        />
      )}
    </div>
  );
}
