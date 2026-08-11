"use client";

import { useState, type CSSProperties } from "react";

import { useDndMonitor } from "@dnd-kit/core";

import { dropStartMin, epLabel, fmtTime, type TimeGridItem } from "@/lib/guide";
import type { GuideResponse } from "@/lib/types";

import { ProviderChip } from "./ProviderChip";
import { usePosterHue } from "./usePosterHue";

export type GridDragPreviewProps = {
  guide: GuideResponse;
  gridItem: TimeGridItem;
  // CalendarView's JS mirror of --hour-px; a ref (not a value) so the
  // preview reads the current scale without re-rendering on resize.
  hourPxRef: { current: number };
  windowStart: number;
  windowEnd: number;
};

// The card rendered inside CalendarView's DragOverlay while a grid item is
// dragged (the source GridItemCard stays in place, dimmed). Sized by the
// overlay wrapper to the source card's measured rect. Carries a live time
// pill: onDragMove recomputes the landing time with the same dropStartMin
// the drop handler uses, so the label can never disagree with where the
// item lands. delta.y arrives pre-snapped to quarter-hour steps by the
// DndContext modifier, so the setState below bails out (same number)
// except when the snapped slot actually changes — per-slot updates, not
// per-pixel (the FullCalendar/Schedule-X gate).
export function GridDragPreview({
  guide,
  gridItem,
  hourPxRef,
  windowStart,
  windowEnd,
}: GridDragPreviewProps) {
  const { item, title, providerName } = gridItem;
  const hue = usePosterHue(item.title_id, title.poster_path);
  const provider = guide.providers[String(item.provider_id)];
  const duration = item.end_min - item.start_min;
  const [startMin, setStartMin] = useState(item.start_min);

  useDndMonitor({
    onDragMove({ delta }) {
      setStartMin(
        dropStartMin(item.start_min, delta.y, hourPxRef.current, windowStart, windowEnd, duration),
      );
    },
  });

  return (
    <div
      className="h-full w-full cursor-grabbing overflow-hidden rounded-xl border border-[hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.55)] bg-[color-mix(in_srgb,hsl(var(--th)_var(--tint-s)_var(--tint-l))_10%,var(--color-panel))] px-2.5 pt-[7px] pb-2 text-left text-ink shadow-[0_0_0_1px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.45),0_10px_28px_-8px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.55)]"
      style={{ "--th": hue } as CSSProperties}
    >
      <span className="mb-1 inline-block rounded-full bg-acc-soft px-2 py-0.5 text-[10px] font-semibold tabular-nums text-acc">
        {fmtTime(startMin)} – {fmtTime(startMin + duration)}
      </span>
      <div className="line-clamp-2 text-[13px] leading-[1.25] font-semibold">{title.name}</div>
      <div className="mt-[3px] flex items-center gap-1 text-[10.5px] text-mut">
        <span className="truncate">{epLabel(title, item)}</span>
        {providerName && (
          <>
            <span aria-hidden="true">·</span>
            <ProviderChip
              variant="inline"
              providerId={item.provider_id}
              logoPath={provider?.logo_path ?? ""}
              providerName={providerName}
            />
          </>
        )}
      </div>
    </div>
  );
}
