"use client";

// Pill-shaped exclusive-choice control (EntryActions' Watchlist/Rotation/
// Watched today; the #18 guide view toggle reuses it). Purely
// presentational — the caller decides what a click on the active option
// means (EntryActions treats it as "clear back to none").
export type SegmentedOption<T extends string> = {
  value: T;
  label: string;
};

export function Segmented<T extends string>({
  options,
  value,
  onChange,
  disabled = false,
  ariaLabel,
}: {
  options: SegmentedOption<T>[];
  value: T;
  onChange: (value: T) => void;
  disabled?: boolean;
  ariaLabel: string;
}) {
  return (
    <div className="flex gap-0.5 rounded-full bg-panel2 p-[3px]" role="group" aria-label={ariaLabel}>
      {options.map((option) => {
        const active = option.value === value;
        return (
          <button
            key={option.value}
            type="button"
            disabled={disabled}
            aria-pressed={active}
            onClick={() => onChange(option.value)}
            className={`rounded-full px-4 py-[7px] text-xs font-semibold whitespace-nowrap disabled:opacity-50 ${
              active ? "bg-ink text-bg" : "text-mut"
            }`}
          >
            {option.label}
          </button>
        );
      })}
    </div>
  );
}
