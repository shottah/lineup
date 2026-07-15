"use client";

// Five stars with half-step hit zones (0.5–5.0): each star overlays an
// amber fill (0/50/100% width) on a gray base and carries two invisible
// half-width buttons. Clicking the current value clears the rating
// (null). Purely presentational; the parent owns persistence.
export function StarRating({
  value,
  onRate,
  disabled = false,
}: {
  value: number | null;
  onRate: (v: number | null) => void;
  disabled?: boolean;
}) {
  const current = value ?? 0;
  return (
    <div className="flex items-center gap-0.5">
      {[1, 2, 3, 4, 5].map((star) => (
        <span key={star} className="relative inline-block h-6 w-6 select-none">
          <span
            aria-hidden
            className="absolute inset-0 text-center text-xl leading-6 text-zinc-300 dark:text-zinc-700"
          >
            ★
          </span>
          <span
            aria-hidden
            className="absolute inset-y-0 left-0 overflow-hidden text-xl leading-6 text-amber-500"
            style={{ width: current >= star ? "100%" : current >= star - 0.5 ? "50%" : "0%" }}
          >
            <span className="block w-6 text-center">★</span>
          </span>
          <button
            type="button"
            disabled={disabled}
            aria-label={`Rate ${star - 0.5} stars`}
            onClick={() => onRate(current === star - 0.5 ? null : star - 0.5)}
            className="absolute inset-y-0 left-0 w-1/2 disabled:cursor-not-allowed"
          />
          <button
            type="button"
            disabled={disabled}
            aria-label={`Rate ${star} stars`}
            onClick={() => onRate(current === star ? null : star)}
            className="absolute inset-y-0 right-0 w-1/2 disabled:cursor-not-allowed"
          />
        </span>
      ))}
      {value != null && <span className="ml-2 text-xs text-zinc-500">{value.toFixed(1)}</span>}
    </div>
  );
}
