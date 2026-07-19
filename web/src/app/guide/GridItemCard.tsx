"use client";

import { type CSSProperties } from "react";

import { useDraggable } from "@dnd-kit/core";

import { epLabel, type TimeGridItem } from "@/lib/guide";
import type { GuideResponse } from "@/lib/types";

import { ItemMenu } from "./ItemMenu";
import { ProviderChip } from "./ProviderChip";
import { SlotQuickActions } from "./SlotQuickActions";
import { usePosterHue } from "./usePosterHue";

export type GridItemCardProps = {
  guide: GuideResponse;
  gridItem: TimeGridItem;
  // The day column this card renders in (= the item's own date/dow) —
  // same role as CalendarSlotCard's columnDate/columnDow, passed straight
  // through to ItemMenu/SlotQuickActions.
  columnDate: string;
  columnDow: string;
  columns: { date: string; dow: string }[];
  minItemPx: number;
  // Whether this card's own day is in the past (design spec §4) — disables
  // dragging and is the only thing distinguishing a past card from a
  // draggable one; the dimmed look itself comes from DayColumn's column-
  // level treatment.
  isPast: boolean;
  open: boolean;
  onToggleOpen: () => void;
  onClose: () => void;
};

// The poster-tinted card from the pre-grid CalendarSlotCard (design spec
// §5) minus the time pill — time now lives in TimeGutter. Absolutely
// positioned inside its DayColumn body from toTimeGrid's topFactor/
// spanFactor/colIndex/colCount; everything else (tint, hover glow/pulse,
// watched dim, Pinned badge, quick-actions cluster, click-to-open
// ItemMenu) is unchanged from the card it replaces.
export function GridItemCard({
  guide,
  gridItem,
  columnDate,
  columnDow,
  columns,
  minItemPx,
  isPast,
  open,
  onToggleOpen,
  onClose,
}: GridItemCardProps) {
  const { item, title, providerName, topFactor, spanFactor, colIndex, colCount } = gridItem;
  const hue = usePosterHue(item.title_id, title.poster_path);
  const watched = item.watched;
  const provider = guide.providers[String(item.provider_id)];

  // Drag-to-move (design spec §3): setNodeRef/transform go on the outer,
  // absolutely-positioned box (the whole slot — quick-actions cluster
  // included — moves together), while listeners/attributes go on the
  // inner click-to-open button only, so it stays the sole focusable
  // element and the drag handle doubles as the existing click target.
  // dnd-kit's PointerSensor activation distance (wired in CalendarView) is
  // what lets a plain click still reach onToggleOpen instead of starting a
  // drag, and keeping listeners off the outer div means the quick-actions
  // buttons (siblings, not descendants of the button) never see them.
  const { attributes, listeners, setNodeRef, transform, isDragging } = useDraggable({
    id: String(item.id),
    disabled: isPast,
  });

  // ItemMenu's Swap/Move pickers need more room than a short/overlapping
  // slot's floored box — while open, the card grows from a min-height
  // instead of being clamped, and floats above its neighbours (higher
  // z-index, visible overflow) rather than clipping the picker UI.
  const boxStyle = {
    "--th": hue,
    top: `calc(var(--hour-px) * ${topFactor})`,
    left: `${(colIndex / colCount) * 100}%`,
    width: `calc(${100 / colCount}% - 4px)`,
    ...(open
      ? { minHeight: `max(calc(var(--hour-px) * ${spanFactor}), ${minItemPx}px)`, zIndex: 30 }
      : { height: `max(calc(var(--hour-px) * ${spanFactor}), ${minItemPx}px)` }),
    ...(transform
      ? { transform: `translate3d(${transform.x}px, ${transform.y}px, 0)`, zIndex: 40 }
      : null),
  } as unknown as CSSProperties;

  return (
    <div
      ref={setNodeRef}
      className={`guide-card group absolute rounded-xl border-[hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.55)] border bg-[color-mix(in_srgb,hsl(var(--th)_var(--tint-s)_var(--tint-l))_7%,var(--color-panel))] transition-[box-shadow,transform,opacity] duration-200 ease-out hover:-translate-y-px focus-within:-translate-y-px hover:shadow-[0_0_0_1px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.45),0_6px_18px_-6px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.4)] focus-within:shadow-[0_0_0_1px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.45),0_6px_18px_-6px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.4)] ${
        open ? "z-30 overflow-visible" : "overflow-hidden"
      } ${watched ? "opacity-50 hover:opacity-100 focus-within:opacity-100" : ""} ${
        isDragging ? "opacity-80 shadow-lg" : ""
      }`}
      style={boxStyle}
    >
      <button
        type="button"
        onClick={onToggleOpen}
        {...listeners}
        {...attributes}
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
      {!open && (
        <SlotQuickActions guideId={guide.id} item={item} title={title} columnDow={columnDow} />
      )}
      {open && (
        <ItemMenu
          guideId={guide.id}
          item={item}
          title={title}
          columnDate={columnDate}
          columnDow={columnDow}
          columns={columns}
          onClose={onClose}
        />
      )}
    </div>
  );
}
