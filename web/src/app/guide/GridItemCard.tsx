"use client";

import { type CSSProperties } from "react";

import { useDraggable } from "@dnd-kit/core";

import { epLabel, type TimeGridItem } from "@/lib/guide";
import type { GuideResponse } from "@/lib/types";

import { ProviderChip } from "./ProviderChip";
import { SlotQuickActions } from "./SlotQuickActions";
import { usePosterHue } from "./usePosterHue";

export type GridItemCardProps = {
  guide: GuideResponse;
  gridItem: TimeGridItem;
  // The day column this card renders in — passed through to
  // SlotQuickActions for the Pin toast ("Pinned to {dow}").
  columnDow: string;
  minItemPx: number;
  // Whether this card's own day is in the past (design spec §4) — disables
  // dragging and is the only thing distinguishing a past card from a
  // draggable one; the dimmed look itself comes from DayColumn's column-
  // level treatment.
  isPast: boolean;
};

// The poster-tinted card from the pre-grid CalendarSlotCard (design spec
// §5) minus the time pill — time now lives in TimeGutter. Absolutely
// positioned inside its DayColumn body from toTimeGrid's topFactor/
// spanFactor/colIndex/colCount. The card has no click action: watched/
// pin/remove live in the hover quick-actions cluster, and press-and-move
// drags the slot to a new time or day (CalendarView's DragOverlay is the
// moving visual — this card never receives the drag transform and stays
// in place as a dimmed placeholder while dragging). setNodeRef goes on
// the outer, absolutely-positioned box; the drag listeners/attributes go
// on the inner button only, so it stays the sole focusable element and
// the quick-actions buttons (siblings, not descendants of it) never see
// them.
export function GridItemCard({ guide, gridItem, columnDow, minItemPx, isPast }: GridItemCardProps) {
  const { item, title, providerName, topFactor, spanFactor, colIndex, colCount } = gridItem;
  const hue = usePosterHue(item.title_id, title.poster_path);
  const watched = item.watched;
  const provider = guide.providers[String(item.provider_id)];

  const { attributes, listeners, setNodeRef, isDragging } = useDraggable({
    id: String(item.id),
    disabled: isPast,
  });

  const boxStyle = {
    "--th": hue,
    top: `calc(var(--hour-px) * ${topFactor})`,
    left: `${(colIndex / colCount) * 100}%`,
    width: `calc(${100 / colCount}% - 4px)`,
    height: `max(calc(var(--hour-px) * ${spanFactor}), ${minItemPx}px)`,
    // Inline so it outranks every opacity class in the cascade — the
    // watched treatment's focus-within:opacity-100 would otherwise win
    // while the drag holds focus on the inner button.
    ...(isDragging ? { opacity: 0.4 } : null),
  } as unknown as CSSProperties;

  return (
    <div
      ref={setNodeRef}
      className={`guide-card group absolute overflow-hidden rounded-xl border-[hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.55)] border bg-[color-mix(in_srgb,hsl(var(--th)_var(--tint-s)_var(--tint-l))_7%,var(--color-panel))] transition-[box-shadow,opacity,translate] duration-200 ease-out hover:-translate-y-px focus-within:-translate-y-px hover:shadow-[0_0_0_1px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.45),0_6px_18px_-6px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.4)] focus-within:shadow-[0_0_0_1px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.45),0_6px_18px_-6px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.4)] ${
        watched ? "opacity-50 hover:opacity-100 focus-within:opacity-100" : ""
      }`}
      style={boxStyle}
    >
      {/* No aria-label: the button's accessible name is its full content
          (title, episode, provider, Pinned), and dnd-kit's attributes add
          the draggable role description. Past cards are disabled — an
          enabled button would be an inert control announcing a false
          affordance, since past items can't be dragged and have no click
          action. */}
      <button
        type="button"
        disabled={isPast}
        {...(isPast ? {} : listeners)}
        {...(isPast ? {} : attributes)}
        className={`block h-full w-full rounded-xl px-2.5 pt-[7px] pb-2 text-left text-ink ${
          isPast ? "" : "cursor-grab active:cursor-grabbing"
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
      </button>
      <SlotQuickActions guideId={guide.id} item={item} title={title} columnDow={columnDow} />
    </div>
  );
}
