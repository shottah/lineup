"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";

import { useToast } from "@/components/Providers";
import { api, ApiError } from "@/lib/api";
import type { GuideResponse } from "@/lib/types";

import { GENERIC_ERROR } from "./useGuideItemMutations";

// Pure cache-mutation helper for the drag optimistic update (design spec
// §3): moves one item to a new date/start_min, recomputing end_min from
// its own (unchanged) duration. Every other item is returned as-is —
// vitest-covered directly (useGuideItemDrag.test.ts) since the DnD wiring
// itself isn't unit-testable in this project's node test environment.
export function moveItemInGuide(
  guide: GuideResponse,
  itemId: number,
  date: string,
  startMin: number,
): GuideResponse {
  return {
    ...guide,
    items: guide.items.map((it) => {
      if (it.id !== itemId) return it;
      const duration = it.end_min - it.start_min;
      return { ...it, date, start_min: startMin, end_min: startMin + duration };
    }),
  };
}

export type UseGuideItemDragParams = {
  guideId: number;
};

export type DragMoveVars = {
  itemId: number;
  date: string;
  startMin: number;
};

// The drag-to-move mutation (design spec §3): PATCHes the moved item's
// date/start_min, writing the new position into the ["guide"] cache
// immediately (onMutate) so the card lands where it was dropped rather
// than waiting on the round trip, then rolling back to the pre-drag
// snapshot if the server rejects the move (the past-slot guards from
// Task 2 — spec §4) and surfacing its message via the same toast helper
// the other item mutations use.
export function useGuideItemDrag({ guideId }: UseGuideItemDragParams) {
  const queryClient = useQueryClient();
  const { show } = useToast();

  return useMutation({
    mutationFn: ({ itemId, date, startMin }: DragMoveVars) =>
      api(`/v1/guides/${guideId}/items/${itemId}`, {
        method: "PATCH",
        body: JSON.stringify({ date, start_min: startMin }),
      }),
    onMutate: async ({ itemId, date, startMin }: DragMoveVars) => {
      await queryClient.cancelQueries({ queryKey: ["guide"] });
      const previous = queryClient.getQueryData<GuideResponse>(["guide"]);
      if (previous) {
        queryClient.setQueryData<GuideResponse>(["guide"], moveItemInGuide(previous, itemId, date, startMin));
      }
      return { previous };
    },
    onError: (err, _vars, context) => {
      if (context?.previous) {
        queryClient.setQueryData(["guide"], context.previous);
      }
      show(err instanceof ApiError && err.status === 422 ? err.code : GENERIC_ERROR);
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ["guide"] });
    },
  });
}
