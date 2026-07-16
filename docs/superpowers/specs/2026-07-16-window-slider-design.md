# Slider viewing-window editor (issue #43) — design

Date: 2026-07-16
Status: approved (user selected the slider + apply-to-all modality from a
three-option preview comparison)
Issue: [#43 feat(web): slider-based viewing-window editor](https://github.com/shottah/lineup/issues/43)

## Problem

Settings requires typing HH:MM into fourteen native time inputs; the user
wants effortless widening/narrowing per day.

## Design

### WindowSlider component (`web/src/app/settings/WindowSlider.tsx`)

A dual-thumb range control built from two stacked native
`<input type="range">` elements (keyboard-accessible for free; no new
dependency). Props:

```ts
{
  startMin: number;      // minutes from midnight
  endMin: number;
  disabled: boolean;     // day toggled off — dimmed, inert
  onChange: (startMin: number, endMin: number) => void;
  dayLabel: string;      // "Mon" — aria labels "{dayLabel} start" / "{dayLabel} end"
}
```

- Track: 960 (16:00) → 1440, `step={30}`. The 1440 stop is the "midnight"
  terminal: displayed via label as `12:00 am`, converted to `"23:59"` at
  the form boundary (the API rejects `24:00` and requires start<end
  within the day).
- Clamping: thumbs may not cross — minimum gap 30 (`start ≤ end − 30`);
  a drag past the other thumb clamps in the onChange handler.
- Visuals (tokens): 4px track `bg-panel2 rounded-full`; the selected
  span an absolutely-positioned `bg-acc` bar between thumb percents;
  16px round thumbs `bg-acc` (border `acc-ink`); disabled → opacity-50.
  Dual-range CSS: inputs stacked `absolute inset-0 appearance-none
  bg-transparent pointer-events-none`, thumbs re-enabled via
  `[&::-webkit-slider-thumb]:pointer-events-auto` (+ `-moz-` twins).
- Track edge captions `4pm` / `12am` (10px faint).

### SettingsBody surgery

- The two `<input type="time">` per row are REPLACED by `WindowSlider` +
  a live label `7:00 pm – 11:00 pm` (13px mut; en dash; formatted with
  `fmtTime` from `@/lib/guide`, with 1440 rendered as `12:00 am`).
- Conversion at the form boundary only: stored `"19:00"` → 1140 for the
  slider; the sentinel round-trips exactly — parse maps `"23:59"` → 1440
  (never 1439), and the label renders 1440 as `12:00 am`; onChange
  minutes → zero-padded `HH:MM` (1440 → `"23:59"`) into the existing
  form state — the document shape, validation, debounce
  auto-save, and unmount flush are UNCHANGED.
- Stored values outside the track or off-step (legacy/hand-set) are
  displayed faithfully in the label; the slider thumb sits at the
  nearest stop but NOTHING mutates until the user drags (auto-save must
  never fire from render).
- Per-row `Apply to all days` text button (12px acc, appears under the
  active row's label — always rendered, one per row): copies that row's
  start/end (NOT its enabled flag) to all seven days in one state
  update → one debounced save. No toast beyond the existing
  `Settings saved`.
- The enabled switch, day layout, and `End must be after start.` error
  line remain (the error is now structurally unreachable from slider
  input; it guards legacy data).

### Verification

`pnpm lint && pnpm test && pnpm build`; manual: drag ends (30-min
snaps), keyboard arrows on a focused thumb, apply-to-all fans out one
save, day-off dimming, saved values round-trip after reload, top stop
saves 23:59 and re-renders as 12:00 am.

## Out of scope

Preset chips; per-day multiple windows; crossing midnight (API
constraint); changing auto-save semantics; API changes.
