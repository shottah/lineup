"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";

import { useToast } from "@/components/Providers";
import { api } from "@/lib/api";
import type { GuideItem, GuideTitleLookup } from "@/lib/types";

export const GENERIC_ERROR = "Couldn't save — try again.";

export type UseGuideItemMutationsParams = {
  guideId: number;
  item: GuideItem;
  title: GuideTitleLookup;
  // The column the item currently renders in, needed only for the Pin
  // toast ("Pinned to {dow}").
  columnDow: string;
};

// Watched/Pin/Remove mutation configs shared by ItemMenu and
// SlotQuickActions (same endpoints, toasts, and invalidations) so the
// hover-shortcut cluster and the full menu stay behaviorally identical.
// Each caller gets its own mutation instances — this shares config, not
// mutation state — per the design addendum's "owns its own instances"
// requirement (CalendarView only mounts one or the other at a time).
export function useGuideItemMutations({ guideId, item, title, columnDow }: UseGuideItemMutationsParams) {
  const queryClient = useQueryClient();
  const { show } = useToast();

  const invalidateGuide = () => queryClient.invalidateQueries({ queryKey: ["guide"] });
  const itemPath = `/v1/guides/${guideId}/items/${item.id}`;

  const watchedM = useMutation({
    mutationFn: () => api(`${itemPath}/watched`, { method: item.watched ? "DELETE" : "POST" }),
    onError: () => show(GENERIC_ERROR),
    onSuccess: () => {
      show(item.watched ? `Unwatched · ${title.name}` : `Watched · ${title.name}`);
      queryClient.invalidateQueries({ queryKey: ["shelf"] });
    },
    onSettled: invalidateGuide,
  });

  const pinM = useMutation({
    mutationFn: () => api(itemPath, { method: "PATCH", body: JSON.stringify({ pinned: !item.pinned }) }),
    onError: () => show(GENERIC_ERROR),
    onSuccess: () => show(item.pinned ? "Unpinned" : `Pinned to ${columnDow}`),
    onSettled: invalidateGuide,
  });

  const removeM = useMutation({
    mutationFn: () => api(itemPath, { method: "DELETE" }),
    onError: () => show(GENERIC_ERROR),
    onSuccess: () => show("Removed — enjoy the free hour"),
    onSettled: invalidateGuide,
  });

  return { show, invalidateGuide, itemPath, watchedM, pinM, removeM };
}
