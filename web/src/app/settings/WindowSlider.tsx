"use client";

import type { ChangeEvent } from "react";
import { fmtTime } from "@/lib/guide";

// Track bounds (minutes from midnight): 16:00 -> 24:00. 1440 is the
// "midnight" terminal stop — the API rejects "24:00" and requires
// start < end within the day, so it round-trips through the sentinel
// "23:59" at the form boundary (see toHHMM/parseHHMM below). Never 1439.
const TRACK_MIN = 960;
const TRACK_MAX = 1440;
const STEP = 30;
const MIN_GAP = 30;

// Form-boundary conversion (HH:MM stored strings <-> slider minutes).
// Lives here (not lib/guide, which is guide-payload mappers) because
// it's purely a slider concern: the 1440 sentinel only exists to give
// the track a reachable "midnight" stop.
export function parseHHMM(hhmm: string): number {
  if (hhmm === "23:59") return TRACK_MAX;
  const [hStr, mStr] = hhmm.split(":");
  const h = Number(hStr);
  const m = Number(mStr);
  return Number.isFinite(h) && Number.isFinite(m) ? h * 60 + m : TRACK_MIN;
}

export function toHHMM(min: number): string {
  if (min === TRACK_MAX) return "23:59";
  const h = Math.floor(min / 60);
  const m = min % 60;
  return `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}`;
}

// Legacy/hand-set values may sit outside the track or off the 30-min
// step. The label renders them faithfully (via fmtTime in SettingsBody);
// the thumb itself snaps to the nearest stop for display only — this
// must never feed back into onChange on its own (no mutation without a
// user drag).
function toDisplay(min: number): number {
  const clamped = Math.min(Math.max(min, TRACK_MIN), TRACK_MAX);
  return Math.round((clamped - TRACK_MIN) / STEP) * STEP + TRACK_MIN;
}

function percentOf(min: number): number {
  return ((min - TRACK_MIN) / (TRACK_MAX - TRACK_MIN)) * 100;
}

export function clampPair(
  start: number,
  end: number,
  changed: "start" | "end"
): [number, number] {
  if (changed === "start") {
    const clamped = Math.min(Math.max(start, TRACK_MIN), TRACK_MAX - MIN_GAP);
    return [clamped, Math.max(end, clamped + MIN_GAP)];
  } else {
    const clamped = Math.max(Math.min(end, TRACK_MAX), TRACK_MIN + MIN_GAP);
    return [Math.min(start, clamped - MIN_GAP), clamped];
  }
}

const THUMB =
  "[&::-webkit-slider-thumb]:pointer-events-auto [&::-webkit-slider-thumb]:appearance-none " +
  "[&::-webkit-slider-thumb]:h-4 [&::-webkit-slider-thumb]:w-4 [&::-webkit-slider-thumb]:rounded-full " +
  "[&::-webkit-slider-thumb]:border-2 [&::-webkit-slider-thumb]:border-acc-ink [&::-webkit-slider-thumb]:bg-acc " +
  "[&::-webkit-slider-runnable-track]:bg-transparent " +
  "[&::-moz-range-thumb]:pointer-events-auto [&::-moz-range-thumb]:appearance-none " +
  "[&::-moz-range-thumb]:h-4 [&::-moz-range-thumb]:w-4 [&::-moz-range-thumb]:rounded-full " +
  "[&::-moz-range-thumb]:border-2 [&::-moz-range-thumb]:border-acc-ink [&::-moz-range-thumb]:bg-acc " +
  "[&::-moz-range-track]:bg-transparent";

const RANGE_INPUT_CLASS =
  `pointer-events-none absolute inset-0 h-4 w-full cursor-pointer appearance-none bg-transparent disabled:cursor-not-allowed ${THUMB}`;

type WindowSliderProps = {
  startMin: number;
  endMin: number;
  disabled: boolean;
  onChange: (startMin: number, endMin: number) => void;
  dayLabel: string;
};

export function WindowSlider({ startMin, endMin, disabled, onChange, dayLabel }: WindowSliderProps) {
  const displayStart = toDisplay(startMin);
  const displayEnd = toDisplay(endMin);

  const handleStart = (e: ChangeEvent<HTMLInputElement>) => {
    const [start, end] = clampPair(Number(e.target.value), displayEnd, "start");
    onChange(start, end);
  };

  const handleEnd = (e: ChangeEvent<HTMLInputElement>) => {
    const [start, end] = clampPair(displayStart, Number(e.target.value), "end");
    onChange(start, end);
  };

  return (
    <div className={disabled ? "opacity-50" : ""}>
      <div className="relative h-4 w-full">
        <div className="absolute top-1/2 h-1 w-full -translate-y-1/2 rounded-full bg-panel2" />
        <div
          className="absolute top-1/2 h-1 -translate-y-1/2 rounded-full bg-acc"
          style={{ left: `${percentOf(displayStart)}%`, right: `${100 - percentOf(displayEnd)}%` }}
        />
        <input
          type="range"
          min={TRACK_MIN}
          max={TRACK_MAX}
          step={STEP}
          value={displayStart}
          disabled={disabled}
          aria-label={`${dayLabel} start`}
          aria-valuetext={fmtTime(displayStart)}
          onChange={handleStart}
          className={RANGE_INPUT_CLASS}
        />
        <input
          type="range"
          min={TRACK_MIN}
          max={TRACK_MAX}
          step={STEP}
          value={displayEnd}
          disabled={disabled}
          aria-label={`${dayLabel} end`}
          aria-valuetext={fmtTime(displayEnd)}
          onChange={handleEnd}
          className={RANGE_INPUT_CLASS}
        />
      </div>
      <div className="mt-0.5 flex justify-between text-[10px] text-faint">
        <span>4pm</span>
        <span>12am</span>
      </div>
    </div>
  );
}
