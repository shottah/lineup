"use client";

import { useEffect, useState } from "react";

import { hashHue, peekPosterHue, posterHue } from "./posterTint";

// Zero-flash hue resolution (design spec §2.2/§2.4, docs/design/
// guide-card-redesign.md): synchronously reads whatever posterTint's
// module-level cache already has resolved for this title_id — or the
// deterministic hash if nothing's resolved yet — so first paint is never
// blocked on image decode. The effect then (re-)runs the async
// canvas-extraction upgrade and re-renders once it resolves.
export function usePosterHue(titleId: number, posterPath: string): number {
  // React's "adjust state during render" pattern (not an effect) for
  // resyncing when a component instance is reused for a different
  // title_id without unmounting — e.g. BoardView swapping a new title
  // into the same cell. Comparing against the titleId this render's hue
  // was resolved for and calling setState here (render phase, not an
  // effect) re-reads the cache before paint, avoiding both a stale-hue
  // flash and a synchronous setState-in-effect.
  const [resolvedFor, setResolvedFor] = useState(titleId);
  const [hue, setHue] = useState(() => peekPosterHue(titleId) ?? hashHue(titleId));
  if (titleId !== resolvedFor) {
    setResolvedFor(titleId);
    setHue(peekPosterHue(titleId) ?? hashHue(titleId));
  }

  useEffect(() => {
    let cancelled = false;
    posterHue(titleId, posterPath).then((resolved) => {
      if (!cancelled) setHue(resolved);
    });
    return () => {
      cancelled = true;
    };
  }, [titleId, posterPath]);

  return hue;
}
