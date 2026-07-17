"use client";

import type { GuideItem, GuideTitleLookup } from "@/lib/types";

import { useGuideItemMutations } from "./useGuideItemMutations";

export type SlotQuickActionsProps = {
  guideId: number;
  item: GuideItem;
  title: GuideTitleLookup;
  // The column the card currently renders in — same role as ItemMenu's
  // columnDow, needed only for the Pin toast ("Pinned to {dow}").
  columnDow: string;
};

// Hover/keyboard shortcut cluster for a calendar slot card (design spec
// docs/design/guide-card-redesign.md Addendum §A.0-A.10): watched/pin
// toggles + one-shot remove. Reuses ItemMenu's exact mutation semantics
// (same endpoints, toasts, invalidations) via the shared
// useGuideItemMutations hook, so the shortcut and the menu stay
// behaviorally identical — this owns its own mutation instances rather
// than borrowing ItemMenu's because CalendarView only mounts one or the
// other (§A.5): the cluster renders while ItemMenu is closed.
export function SlotQuickActions({ guideId, item, title, columnDow }: SlotQuickActionsProps) {
  const { watchedM, pinM, removeM } = useGuideItemMutations({ guideId, item, title, columnDow });

  const busy = watchedM.isPending || pinM.isPending || removeM.isPending;

  return (
    <div className="pointer-events-none absolute right-1.5 top-1.5 z-10 flex items-center gap-px rounded-full border border-line bg-panel/95 p-0.5 opacity-0 shadow-sm backdrop-blur-sm transition-opacity duration-150 group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100">
      <button
        type="button"
        aria-pressed={item.watched}
        aria-label={item.watched ? "Mark as not watched" : "Mark as watched"}
        title={item.watched ? "Mark as not watched" : "Mark as watched"}
        disabled={busy}
        onClick={() => watchedM.mutate()}
        className={`inline-flex h-6 w-6 items-center justify-center rounded-full transition-colors disabled:pointer-events-none disabled:opacity-50 ${
          item.watched ? "bg-acc-soft text-acc" : "text-mut hover:bg-panel2 hover:text-ink"
        }`}
      >
        <svg
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2.5"
          strokeLinecap="round"
          strokeLinejoin="round"
          className="h-[13px] w-[13px]"
          aria-hidden="true"
        >
          <path d="M20 6 9 17l-5-5" />
        </svg>
      </button>

      <button
        type="button"
        aria-pressed={item.pinned}
        aria-label={item.pinned ? "Unpin" : "Pin"}
        title={item.pinned ? "Unpin" : "Pin"}
        disabled={busy}
        onClick={() => pinM.mutate()}
        className={`inline-flex h-6 w-6 items-center justify-center rounded-full transition-colors disabled:pointer-events-none disabled:opacity-50 ${
          item.pinned ? "bg-acc-soft text-acc" : "text-mut hover:bg-panel2 hover:text-ink"
        }`}
      >
        <svg
          viewBox="0 0 24 24"
          fill={item.pinned ? "currentColor" : "none"}
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          className="h-[14px] w-[14px]"
          aria-hidden="true"
        >
          <path d="M12 17v5" />
          <path d="M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V7a1 1 0 0 1 1-1 2 2 0 0 0 0-4H8a2 2 0 0 0 0 4 1 1 0 0 1 1 1z" />
        </svg>
      </button>

      <span className="mx-[1px] h-3.5 w-px bg-line" aria-hidden="true"></span>

      <button
        type="button"
        aria-label="Remove"
        title="Remove"
        disabled={busy}
        onClick={() => removeM.mutate()}
        className="inline-flex h-6 w-6 items-center justify-center rounded-full text-danger transition-colors hover:bg-danger/15 disabled:pointer-events-none disabled:opacity-50"
      >
        <svg
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2.5"
          strokeLinecap="round"
          strokeLinejoin="round"
          className="h-[12px] w-[12px]"
          aria-hidden="true"
        >
          <path d="M18 6 6 18M6 6l12 12" />
        </svg>
      </button>
    </div>
  );
}
