"use client";

import { useState, type CSSProperties } from "react";

import { useDndMonitor } from "@dnd-kit/core";

import { dropStartMin, epLabel, fmtTime, type TimeGridItem } from "@/lib/guide";
import type { GuideResponse } from "@/lib/types";

import { ProviderChip } from "./ProviderChip";
import { usePosterHue } from "./usePosterHue";

export type GridDragPreviewProps = {
  guide: GuideResponse;
  // Captured at drag start by CalendarView — deliberately not re-resolved
  // from the live guide, so a mid-drag cache update can't change or
  // vanish the ghost.
  gridItem: TimeGridItem;
  // CalendarView's JS mirror of --hour-px; a ref (not a value) so the
  // preview reads the current scale without re-rendering on resize.
  hourPxRef: { current: number };
  windowStart: number;
  windowEnd: number;
  minItemPx: number;
};

// The card rendered inside CalendarView's DragOverlay while a grid item is
// dragged (the source GridItemCard stays in place, dimmed). Always the
// collapsed card size — computed from --hour-px so an expanded (open)
// source doesn't stretch the ghost — with the live time pill floating
// ABOVE the box, where it can't eat a short card's only text row. The
// pill recomputes on onDragMove with the same dropStartMin the drop
// handler uses, so it can never disagree with where the item lands; the
// setState bails out (same number) except when the snapped slot actually
// changes, keeping updates per-slot rather than per-pixel.
export function GridDragPreview({
  guide,
  gridItem,
  hourPxRef,
  windowStart,
  windowEnd,
  minItemPx,
}: GridDragPreviewProps) {
  const { item, title, providerName, spanFactor } = gridItem;
  const hue = usePosterHue(item.title_id, title.poster_path);
  const provider = guide.providers[String(item.provider_id)];
  const watched = item.watched;
  const duration = item.end_min - item.start_min;
  // Initial pill = the zero-delta landing slot; with deltaY 0 the hour
  // scale cancels out, so pass 1 rather than reading the ref in render.
  const [startMin, setStartMin] = useState(() =>
    dropStartMin(item.start_min, 0, 1, windowStart, windowEnd, duration),
  );

  useDndMonitor({
    onDragMove({ delta }) {
      setStartMin(
        dropStartMin(item.start_min, delta.y, hourPxRef.current, windowStart, windowEnd, duration),
      );
    },
  });

  return (
    <div
      className="relative w-full cursor-grabbing"
      style={
        {
          "--th": hue,
          height: `max(calc(var(--hour-px) * ${spanFactor}), ${minItemPx}px)`,
        } as CSSProperties
      }
    >
      <span className="absolute -top-7 left-0 rounded-full border border-line bg-panel px-2 py-0.5 text-[10px] font-semibold whitespace-nowrap tabular-nums text-acc shadow-sm">
        {fmtTime(startMin)} – {fmtTime(startMin + duration)}
      </span>
      <div
        className={`h-full w-full overflow-hidden rounded-xl border border-[hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.55)] bg-[color-mix(in_srgb,hsl(var(--th)_var(--tint-s)_var(--tint-l))_10%,var(--color-panel))] px-2.5 pt-[7px] pb-2 text-left text-ink shadow-[0_0_0_1px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.45),0_10px_28px_-8px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.55)] ${
          watched ? "opacity-60" : ""
        }`}
      >
        <div className="line-clamp-2 text-[13px] leading-[1.25] font-semibold">
          {watched ? "✓ " : ""}
          {title.name}
        </div>
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
        {item.pinned && (
          <span className="mt-1.5 inline-block rounded-full bg-acc-soft px-2 py-0.5 text-[9.5px] font-medium text-acc">
            Pinned
          </span>
        )}
      </div>
    </div>
  );
}
