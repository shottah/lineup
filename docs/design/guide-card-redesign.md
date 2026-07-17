# Guide slot card redesign — design spec

Scope: `web/src/app/guide/CalendarView.tsx` (calendar slot cards) and
`web/src/app/guide/BoardView.tsx` (board plan/alternate cells). Implements four
product asks: time-as-pill, logo chips instead of provider names,
poster-derived card color, hover pulse/glow. Grounded in
`docs/design/lineup-app.dc.html`'s tokens and pill vocabulary.

No code files are produced by this spec — every class string and CSS block
below is meant to be copied verbatim into the real components.

---

## 0. Five headline decisions (read this first)

1. **Provider identity is a logo chip in a neutral plate whose polarity
   (light or dark) is chosen by sampling the *logo's own* pixels, not the
   app theme.** Reuses the same canvas-sampling infrastructure built for
   poster color. Text fallback (current behavior) when `logo_path` is empty
   or the image fails to load.
2. **Poster color contributes hue only.** Saturation and lightness are fixed,
   theme-scoped constants (new `--tint-s` / `--tint-l` custom properties
   living in the existing `[data-lt="dark"]` / `[data-lt="light"]` blocks in
   `globals.css`) — so a card's tint is always legible against `panel`
   regardless of how bright or dark the source poster is. Applied as a 1px
   tinted border + ~7% background wash on calendar cards; background-wash-only
   on board cells (subordinate on alternates, and on plan cells because
   `border-acc` there is a *selection* signal that must not be diluted by
   decoration).
3. **Time becomes a neutral bordered pill** (`border-line` + `panel2` fill +
   `mut` text) — deliberately *not* accent-colored, because `acc` is already
   the app's vocabulary for "pinned / selected / your pick" and must not be
   diluted by reuse on a plain metadata pill.
4. **Hover = a static tinted glow using the card's own poster hue, plus a
   slow (2.4s) breathing pulse that fully disables under
   `prefers-reduced-motion`** (the static glow remains). Only applied to
   elements that are actually clickable — the non-interactive board plan
   cell and inert alternate cell get the color treatment but never the hover
   state, so nothing implies an affordance that isn't there.
5. **Deterministic fallback color is a multiplicative hash of `title_id` →
   hue**, rendered synchronously on first paint (zero flash-of-uncolored-card)
   and silently upgraded in place once the real poster is sampled — no
   transition on the swap beyond the card's existing 200ms shadow/transform
   transition catching it for free.

---

## 1. New design tokens

Add to `web/src/app/globals.css`, inside the existing theme blocks (these sit
next to `--acc`, `--ink`, etc. — same mechanism, so components never branch
on theme in JS):

```css
[data-lt="dark"] {
  /* ...existing tokens... */
  --tint-s: 40%;
  --tint-l: 50%;
}

[data-lt="light"] {
  /* ...existing tokens... */
  --tint-s: 48%;
  --tint-l: 42%;
}
```

Rationale for the split values: dark theme needs a slightly *lower*
saturation and *higher* lightness to read as a gentle wash against dark
panels without turning neon; light theme needs a bit more saturation and
lower lightness so the same hue doesn't bleach out against white panels. Both
bands are deliberately narrow (nowhere near 0% or 100% on either axis) so the
hue is always recognizable as color, never drifting to near-gray or
near-white/black.

Every card/cell that needs a poster tint sets exactly one inline custom
property, the hue:

```
style={{ "--th": hue } as CSSProperties}
```

`hue` is an integer 0–359. Nothing else about theme is the component's
concern — `var(--th)`, `var(--tint-s)`, `var(--tint-l)` compose into
`hsl(...)` inside Tailwind arbitrary values, and the cascade handles theme
adaptation the same way it already does for every other token.

---

## 2. Poster color extraction algorithm

### 2.1 Data prerequisite

`GuideTitleLookup` (`web/src/lib/types.ts`) currently has no `poster_path`.
Add it:

```ts
export type GuideTitleLookup = {
  name: string;
  kind: "movie" | "series";
  tmdb_id: number;
  poster_path: string;
};
```

(Mirrors the approved API change — API adds the field verbatim, client does
not rename it.)

### 2.2 Cache

Module-level, in-memory, keyed by `title_id` (not `poster_path` — `title_id`
is the key already threaded everywhere else in the guide types, and it means
every card for the same title across the whole week shares one color, which
reads as a nice, intentional property rather than a coincidence):

```
Map<number, { hue: number; source: "extracted" | "fallback" }>
```

Populated **synchronously** the first time a card for that title renders,
with the fallback hue (step 2.4) — so first paint is never blocked on image
decode. Overwritten asynchronously once real extraction resolves (2.3),
which bumps a subscriber (a tiny `useSyncExternalStore`-style hook,
`usePosterHue(titleId, posterPath)`) so mounted cards re-render once with the
upgraded hue. No new dependency: `Map` + `useSyncExternalStore` are both
built in.

### 2.3 Extraction (runs once per title_id, only when a poster_path exists)

1. `const img = new Image(); img.crossOrigin = "anonymous"; img.src = posterUrl(poster_path, "w92")`.
2. On `load`: draw to an **offscreen 12×18 canvas** (2:3, matches poster
   aspect). Drawing at this tiny size is itself the noise-reduction step —
   the browser's own downscale filtering pre-averages the image, so no
   separate blur pass is needed.
3. `getImageData` over all 216 pixels. For each pixel, convert RGB → HSL.
4. **Filter** out pixels that are near-gray (`saturation < 0.15`) or at the
   lightness extremes (`lightness < 0.08 || lightness > 0.92`) — these are
   almost always letterboxing, matte, or blown highlights, not "poster
   color."
5. If **fewer than 10%** of pixels survive the filter (e.g. a black-and-white
   poster), abandon extraction and keep the fallback hue already in the
   cache — averaging a handful of near-gray survivors produces a muddy,
   meaningless hue, so it's better to keep the clean deterministic fallback
   than a noisy real one.
6. Otherwise compute the **circular mean hue** of the surviving pixels
   (convert each hue to a unit vector, average the vectors, `atan2` back —
   plain arithmetic mean of hue degrees is wrong across the 359°→0°
   wraparound). That's the only number kept; saturation/lightness are
   discarded in favor of the fixed theme tokens from §1.
7. Store `{ hue, source: "extracted" }` in the cache, notify subscribers.
8. On `error` (network failure, decode failure, or a `getImageData` security
   error if a proxy ever fails to send CORS headers) — leave the fallback
   hue in place, mark the title as `source: "fallback"` permanently for this
   session (don't retry every re-render).

### 2.4 Deterministic fallback

```
hue = (title_id * 2654435761) % 360
```

(Knuth multiplicative hash — cheap, well distributed, stable across
sessions/users without persisting anything.) Uses the *same* `--tint-s` /
`--tint-l` bands as extracted colors, so a fallback card is visually
indistinguishable in *kind* from an extracted one — just a different, still
stable, hue.

### 2.5 Application (exact, per surface)

| Surface | Border | Background |
|---|---|---|
| Calendar slot card | `border-[hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.55)]` (1px) | `bg-[color-mix(in_srgb,hsl(var(--th)_var(--tint-s)_var(--tint-l))_7%,var(--color-panel))]` |
| Board **plan** cell | unchanged — keep `border-acc` (selection semantic, must not compete with decoration) | `bg-[color-mix(in_srgb,hsl(var(--th)_var(--tint-s)_var(--tint-l))_7%,var(--color-panel))]` |
| Board **alternate** cell (interactive or inert) | none | `bg-[color-mix(in_srgb,hsl(var(--th)_var(--tint-s)_var(--tint-l))_4%,var(--color-panel2))]` |

**Why 7% / 4%, not more:** `panel` in dark theme is `#1e2127` (L≈13%);
mixing in 7% of a ~50%-lightness hue moves effective lightness by roughly
`0.07 × (50 − 13) ≈ 2.6` points — negligible against the existing
`ink`-on-`panel` contrast ratio (~13:1, far above the WCAG AA floor and the
app's own recorded waiver territory). In light theme `panel` is `#ffffff`
(L=100%); the same 7% mix moves it down by about the same small margin. The
wash is a color hint, never a legibility risk in either theme. Alternates get
half that (4% into the already-slightly-darker `panel2`) specifically to
stay quieter than the plan row, per the "alternates stay visually
subordinate" requirement — no border at all reinforces that they're the
lesser element.

**Watched dimming needs no changes.** `opacity-50` is already applied to the
calendar card's *outer* wrapper, which now also owns the border and
background-wash classes — opacity multiplies through the whole subtree, so a
watched card's tint (and, per §4, its hover glow) fades proportionally for
free.

---

## 3. Time pill

Replaces the current plain-text time line. Deliberately reuses the app's
*neutral* chip material (`panel2` + `line`), not `acc` — `acc` stays reserved
for Pinned/selected/step-number meaning, and giving the time pill the accent
color would blur that distinction the first time both pills appear on the
same card.

```html
<div class="mt-0 inline-flex items-center gap-[3px] rounded-full border border-line bg-panel2/70 px-2 py-[3px]">
  <span class="text-[10px] font-semibold tabular-nums text-mut">8:00</span>
  <span class="text-[8.5px] font-medium text-faint">pm</span>
</div>
```

- `tabular-nums` keeps digit widths constant so a column of pills doesn't
  jitter in width.
- The `/70` translucency on `bg-panel2` (not a flat fill) means the pill
  reads consistently whether it's sitting on a plain `panel` card or —once
  the poster wash is added — a very slightly tinted one; a flat fill would
  need a second theme×tint contrast check, translucency sidesteps it.
- Meridiem split into its own low-emphasis span mirrors the existing
  "count" sub-span pattern in the profile shelf tabs
  (`<span style="font-weight:400;opacity:.65">`).

**Board cells do not get a time pill.** The board's column header already
labels each hour; repeating it inside every cell would be noise the calm
minimal voice doesn't want. This is a deliberate scope cut, not an oversight.

---

## 4. Hover: pulse + glow

### 4.1 Keyframes (add to `globals.css`)

```css
@keyframes guide-card-pulse {
  0%, 100% {
    box-shadow:
      0 0 0 1px hsl(var(--th) var(--tint-s) var(--tint-l) / 0.45),
      0 6px 18px -6px hsl(var(--th) var(--tint-s) var(--tint-l) / 0.4);
  }
  50% {
    box-shadow:
      0 0 0 1.5px hsl(var(--th) var(--tint-s) var(--tint-l) / 0.6),
      0 8px 24px -6px hsl(var(--th) var(--tint-s) var(--tint-l) / 0.55);
  }
}

@keyframes guide-alt-pulse {
  0%, 100% {
    box-shadow:
      0 0 0 1px hsl(var(--th) var(--tint-s) var(--tint-l) / 0.3),
      0 4px 14px -6px hsl(var(--th) var(--tint-s) var(--tint-l) / 0.3);
  }
  50% {
    box-shadow:
      0 0 0 1px hsl(var(--th) var(--tint-s) var(--tint-l) / 0.42),
      0 6px 18px -6px hsl(var(--th) var(--tint-s) var(--tint-l) / 0.42);
  }
}

@media (prefers-reduced-motion: no-preference) {
  .guide-card:hover,
  .guide-card:focus-within {
    animation: guide-card-pulse 2.4s ease-in-out infinite;
  }
  .guide-alt-cell:hover,
  .guide-alt-cell:focus-visible {
    animation: guide-alt-pulse 2.4s ease-in-out infinite;
  }
}
```

**Why 2.4s ease-in-out, not faster/springier:** anything under ~1.5s or with
back/elastic easing reads as "notification, look at me now" — wrong register
for a calm planning tool. 2.4s ease-in-out reads as breathing, not alerting.
`prefers-reduced-motion` fully removes the `animation` declaration; the
0%/100% keyframe values are duplicated as the plain `hover:`/`focus-within:`
Tailwind shadow classes below, so reduced-motion users still get the glow —
just the static form, not the pulse. The two keyframes only differ in
intensity (0.3–0.42 vs 0.45–0.6 alpha), keeping alternates visibly quieter
than the calendar card even while both are being hovered.

### 4.2 Calendar slot card — exact classes

The outer wrapper (today: `rounded-xl bg-panel {watched ? "opacity-50" : ""}`)
becomes:

```
guide-card group relative rounded-xl
border-[hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.55)] border
bg-[color-mix(in_srgb,hsl(var(--th)_var(--tint-s)_var(--tint-l))_7%,var(--color-panel))]
transition-[box-shadow,transform] duration-200 ease-out
hover:-translate-y-px focus-within:-translate-y-px
hover:shadow-[0_0_0_1px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.45),0_6px_18px_-6px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.4)]
focus-within:shadow-[0_0_0_1px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.45),0_6px_18px_-6px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.4)]
{watched ? "opacity-50" : ""}
```

with `style={{ "--th": hue }}` on the same element.

- Hover/focus are bound with `hover:`/`focus-within:` on the **outer div**,
  not the inner button — CSS `:hover` on a parent already fires for the
  whole box regardless of which descendant the pointer is over, and
  `:focus-within` catches the inner button (or, once open, the `ItemMenu`
  chips) receiving keyboard focus. No `:has()` or `group`/`peer` plumbing
  needed.
- The 1px `-translate-y-px` lift is new vocabulary for this app (no existing
  component does a hover-lift) — intentionally tiny (1px) so it reads as
  "responsive" without any risk of overlapping a neighboring card in the
  7-column grid.
- Because binding is on the outer wrapper, the glow **persists while the
  `ItemMenu` is open** and the pointer is still over the card — that's
  correct, not noisy: the `ItemMenu` chips have their own distinct
  `bg-panel2` hover state and visually sit *inside* the glowing boundary
  without competing with it.
- Default `outline: 2px solid var(--acc)` on `:focus-visible` (already
  global, in `globals.css`) is untouched and layers on top of the poster
  glow — the accent focus ring is the accessibility contract and must never
  be replaced by decoration; box-shadow and outline are different paint
  layers so there's no conflict.
- **Watched + hover together:** no special-casing. `opacity-50` on the same
  element the glow paints on means a watched card's hover glow is
  automatically half-intensity — exactly the "watched cards recede" reading
  we want, for free.

### 4.3 Board plan cell — no hover state

Plan cells (`cell.item.is_plan`) are a `<div>`, not a button, and stay that
way — they are display-only by design (per the existing code comment). They
get the background wash from §2.5 and keep `border-acc` untouched, but **no**
`guide-card`/`guide-alt-cell` class and no hover classes at all. Giving a
non-interactive element a hover affordance would be misleading; that
overrides "consistency" here.

### 4.4 Board alternate cell — exact classes

Only the **swappable** alternate (rendered as a real `<button>` today, i.e.
`cell.swapTargetId !== undefined`) gets the interactive treatment:

```
guide-alt-cell block h-full w-full rounded-xl px-3 py-2.5 text-left
bg-[color-mix(in_srgb,hsl(var(--th)_var(--tint-s)_var(--tint-l))_4%,var(--color-panel2))]
transition-shadow duration-200 ease-out
hover:shadow-[0_0_0_1px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.3),0_4px_14px_-6px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.3)]
focus-visible:shadow-[0_0_0_1px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.3),0_4px_14px_-6px_hsl(var(--th)_var(--tint-s)_var(--tint-l)/0.3)]
disabled:opacity-50
```

with `style={{ "--th": hue }}`. Note: **no** `-translate-y-px` on board
cells — the grid packs cells edge-to-edge with only `gap-1.5`, and a lift
here risks visually lapping over the adjacent row/column; glow alone is the
safer motion budget in a dense grid.

The **inert** alternate (`cell.swapTargetId === undefined`, plain `<div>`
today) keeps the same background-wash class as its interactive sibling for
visual consistency of "this is title X's card," but — same logic as the plan
cell — no `guide-alt-cell` class, no hover classes, since it isn't
clickable.

---

## 5. Logo chip

### 5.1 Plate polarity detection

Reuses the exact canvas machinery from §2.3, applied to the *logo* raster
instead of the poster, to answer one binary question: is this logo mark
light-on-transparent (needs a dark plate) or colored/dark-on-transparent
(needs a light plate)? This is deliberately **independent of the app's own
light/dark theme** — the plate's only job is contrast against the logo
pixels, and app theme is irrelevant to that.

1. Cache: `Map<providerId, "plate-light" | "plate-dark" | "text-fallback">`,
   populated once per provider (there are only a handful of distinct
   providers across any guide, so this is essentially free after first use).
2. Load `posterUrl(logo_path, "w92")` with `crossOrigin="anonymous"`.
3. On `load`: draw to an 8×8 offscreen canvas (stretched — shape doesn't
   matter for a luminance read, only tone does).
4. For every pixel with alpha > 10 (skip fully-transparent padding),
   accumulate `luminance = 0.2126R + 0.7152G + 0.0722B` (0–1 scale) weighted
   by alpha.
5. `meanLuminance = Σ(L·α) / Σ(α)`. If `meanLuminance > 0.75` → classify
   **light mark** → `plate-dark`. Otherwise → **dark/colored mark** →
   `plate-light`.
6. On `error` (load failure, decode failure, or a tainted-canvas security
   error) → cache `"text-fallback"` for that provider; the component renders
   the current plain-text form (`epLabel · providerName`) instead of a chip,
   and does not retry on subsequent renders this session.
7. If `logo_path` is empty to begin with, skip sampling entirely and go
   straight to text form — no network request for a URL that doesn't exist.

### 5.2 Plate colors

Fixed, theme-invariant swatches (not tokens) — the chip is meant to read as
a small branded badge sitting *on* a themed card, the same way a Slack/Spotify
integration badge stays a fixed color regardless of host app theme:

- `plate-light`: `#EDEAE3` (warm paper neutral)
- `plate-dark`: `#22252C` (soft ink neutral)

Both get a `border border-line` hairline — necessary because `plate-dark`
against the dark theme's `panel` (`#1e2127`) is close in lightness and would
otherwise nearly disappear; the hairline (which is `ink`-relative, not
`panel`-relative) guarantees edge definition in every theme × plate
combination without needing four separate swatches.

### 5.3 Calendar sub-line — exact markup/classes

Today `slot.sub` is a single pre-joined string (`"S1E5 · Netflix"`) from
`toCalendarColumns`. The chip needs the provider identity as a real element,
so the sub-line changes from one text node to a flex row built from
`epLabel` and the provider lookup (`slot.item.provider_id` →
`guide.providers[...]`) separately, rather than the joined string:

```html
<div class="mt-[3px] flex items-center gap-1 text-[10.5px] text-mut">
  <span>S1E5</span>
  <span aria-hidden="true">·</span>
  <!-- logo present and loaded: -->
  <span class="inline-flex h-4 w-4 shrink-0 items-center justify-center rounded-[5px] border border-line bg-[#EDEAE3]">
    <img src="{logoUrl}" alt="Netflix" title="Netflix" class="h-[11px] w-[11px] object-contain" />
  </span>
  <!-- logo absent/failed: -->
  <span>Netflix</span>
</div>
```

(`bg-[#EDEAE3]` swaps to `bg-[#22252C]` when the cached polarity is
`plate-dark`.) The `<img alt="Netflix">` carries the accessible name in
place, since it sits inline inside a sentence a screen reader already reads
left to right — no extra `aria-label` wrapper needed here.

### 5.4 Board row header — exact markup/classes

This is the row's *sole* provider identifier (today: plain text
`row.providerName`), so it gets a larger, standalone chip — legible without
neighboring text — with an explicit `aria-label` since it isn't embedded in
a sentence:

```html
<span
  role="img"
  aria-label="Netflix"
  title="Netflix"
  class="inline-flex h-[22px] w-[22px] shrink-0 items-center justify-center rounded-[6px] border border-line bg-[#EDEAE3]"
>
  <img src="{logoUrl}" alt="" class="h-3.5 w-3.5 object-contain" />
</span>
<!-- fallback: keep the current text node unchanged, unstyled -->
```

### 5.5 Fallback chain (both surfaces, definitive)

1. `logo_path` empty → text form, no request made.
2. `logo_path` present, image loads, polarity sampled → chip (light or dark
   plate per §5.2).
3. `logo_path` present but image fails to load or polarity sampling errors →
   text form, cached so it isn't retried every re-render.

This matches the hard constraint exactly: chip is additive, text is always
the safety net.

---

## 6. Full state matrix

| State | Calendar slot card | Board plan cell | Board alternate (swappable) | Board alternate (inert) |
|---|---|---|---|---|
| Default | `panel` + 7% hue wash, 1px hue border, neutral time pill, logo chip/text | `panel` + 7% hue wash, `border-acc`, step badge | `panel2` + 4% hue wash | `panel2` + 4% hue wash |
| Hover | static glow + 2.4s pulse (motion-gated) + 1px lift | *(none — not interactive)* | static glow (lower alpha) + 2.4s pulse (motion-gated), no lift | *(none — not interactive)* |
| Focus-visible / focus-within | same visual as hover + global `acc` outline (unchanged) | n/a | same as hover + global `acc` outline | n/a |
| Pinned | unchanged `acc-soft`/`acc` pill below title, independent of tint | n/a | n/a | n/a |
| Watched | `opacity-50` on the same element that carries border/bg/glow → everything dims together, no extra rules | n/a (plan cells have no watched state today) | n/a | n/a |
| `open` (ItemMenu expanded) | glow persists if pointer/focus remains inside the wrapper; `ItemMenu` chips keep their own `panel2` hover, unaffected | n/a | n/a | n/a |
| `prefers-reduced-motion: reduce` | glow present, pulse animation absent (static 0%/100% shadow only) | n/a (no hover regardless) | glow present, pulse absent | n/a |
| Disabled (`swapPending`) | n/a | n/a | existing `disabled:opacity-50` retained, glow still computed but visually muted by the same opacity | n/a |

---

## 7. Explicitly out of scope

- `ItemMenu.tsx` chip styling (Watched/Pin/Swap/Move/Details/Remove) is
  unchanged — those are action chips, a different vocabulary from the
  metadata/time pill, and already consistent.
- The "Airs live" pill shown in `lineup-app.dc.html`'s mock isn't implemented
  in `CalendarView.tsx` today; this spec doesn't add it. If it ships later it
  should keep the mock's solid `acc`/`acc-ink` treatment (a true live/urgent
  state, unlike the neutral time pill above) and sit to the right of the
  time pill, not replace it.
- `TitleCard.tsx` and the title detail page's provider row
  (`TitleBody.tsx`) are not touched — the logo-chip treatment here is scoped
  to the guide surfaces the product owner called out. A follow-up could
  bring `TitleBody.tsx`'s provider row onto the same chip mechanism, but
  that page's providers list is a different information shape (canonical
  availability list, not a single per-slot identity) and deserves its own
  pass.
