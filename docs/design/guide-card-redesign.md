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
  we want, for free. (Refined once the hover quick actions land: a watched
  card un-dims to full opacity *while hovered/focused* so the revealed
  action buttons clear CSS's opacity cap — see **Addendum §A.6**. The glow is
  then full-intensity during that hover, by design; the recede reading is
  preserved at rest, which is when it matters.)

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

---

## Addendum: hover quick actions (watched / pin / remove)

Adds three icon-only shortcut buttons to each **calendar slot card** —
*mark watched*, *pin*, *remove* — revealed on hover/focus so the three
highest-frequency actions no longer require clicking the card open and
drilling into `ItemMenu`. Swap, Move, and Details stay in `ItemMenu` only
(they are multi-step and lower-frequency — see §A.10). Scope is
`CalendarView.tsx` **only**: the board has no per-item menu to shortcut
(plan cells are display-only per §4.3; alternate cells already own a
swap-on-click gesture), so it gets no cluster (§A.7).

This composes with — does not replace — the hover glow/pulse (§4), the tint
system (§1–2), the time pill (§3), and the logo chip (§5). The cluster is a
foreground overlay; the glow is a box-shadow on the wrapper; they share the
same `:hover`/`:focus-within` trigger and never paint on each other.

### A.0 Five decisions (read this first)

1. **A single segmented pill, pinned to the card's top-right corner, inside
   the time-pill band** — never the title band. The title is the one thing
   the user explicitly refused to see occluded; anchoring the cluster in the
   ~24px strip the time pill already lives in (well above the title's top
   edge) makes title occlusion structurally impossible regardless of how many
   lines the title wraps to.
2. **Reuses the existing `group relative` wrapper from §4.2** — the cluster is
   an absolutely-positioned *sibling* of the card's main button, revealed with
   the same `group-hover`/`group-focus-within` mechanism as
   `WatchlistQuickActions`, adapted from a poster scrim to a compact corner
   chip because guide cards are text, not posters.
3. **Watched and pin are stateful toggles** (`aria-pressed`, `acc`-family
   active treatment) reusing `ItemMenu`'s exact mutation semantics; **remove
   is a one-shot destructive action** (danger token, set apart by a hairline
   divider), mirroring `ItemMenu`'s immediate Remove.
4. **On a watched card the whole card un-dims to full opacity while
   hovered/focused** — the only way to lift revealed controls out of the
   `opacity-50` cap (CSS opacity composites the whole subtree; a child can
   never exceed its parent's effective opacity). See §A.6.
5. **Inline SVG glyphs only** — check, pin (outline↔filled), X — each pinned
   to an exact `viewBox` and stroke treatment below (§A.4). No icon library.

### A.1 Placement & geometry

The cluster is a sibling of the main `<button>`, mounted inside the
`guide-card group relative` wrapper (§4.2), and rendered **only when this
card's `ItemMenu` is closed** (`{!open && ( … )}` — see §A.5, open state).

| Property | Value | Why |
|---|---|---|
| Anchor | `absolute right-1.5 top-1.5` (6px inset from top-right) | Sits in the time-pill band, right-aligned |
| Vertical extent | top ≈ 6px, height ≈ 28px → bottom ≈ 34px | The time pill occupies ≈ y11–31; the title starts at ≈ y34 and grows **downward**, so the cluster never reaches the title even on a 2-line title |
| Stacking | `z-10` | Above the card body so pointer events hit the buttons, never the main button beneath |
| Footprint | ≈ 80px wide (`p-0.5` + three `h-6 w-6` buttons + a `w-px` divider) | Fits beside a ~50px time pill on the narrowest 160px column (136px content) |

**Time-pill overlap on the tightest columns:** on sub-130px content widths
(e.g. a 7-col desktop grid on a ~1024px container) the revealed cluster may
overlap the time pill's *trailing* edge by a few px. Accepted: the cluster is
hover-only, the pill's digits are left-aligned and stay legible, and — the
binding constraint — the **title is never touched**. The alternative
(reserving flow space for the cluster at rest) would permanently shrink the
pill even when the cluster is hidden; an absolute overlay costs nothing at
rest.

### A.2 Exact markup (copy verbatim)

```html
<!-- sibling of the card's main <button>, inside `guide-card group relative`;
     render only when this card's ItemMenu is closed: {!open && ( … )} -->
<div
  class="pointer-events-none absolute right-1.5 top-1.5 z-10 flex items-center gap-px rounded-full border border-line bg-panel/95 p-0.5 opacity-0 shadow-sm backdrop-blur-sm transition-opacity duration-150 group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100"
>
  <!-- Watched toggle -->
  <button
    type="button"
    aria-pressed={item.watched}
    aria-label={item.watched ? "Mark as not watched" : "Mark as watched"}
    title={item.watched ? "Mark as not watched" : "Mark as watched"}
    disabled={busy}
    onClick={() => watchedM.mutate()}
    class={`inline-flex h-6 w-6 items-center justify-center rounded-full transition-colors disabled:pointer-events-none disabled:opacity-50 ${
      item.watched ? "bg-acc-soft text-acc" : "text-mut hover:bg-panel2 hover:text-ink"
    }`}
  >
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" class="h-[13px] w-[13px]" aria-hidden="true">
      <path d="M20 6 9 17l-5-5" />
    </svg>
  </button>

  <!-- Pin toggle -->
  <button
    type="button"
    aria-pressed={item.pinned}
    aria-label={item.pinned ? "Unpin" : "Pin"}
    title={item.pinned ? "Unpin" : "Pin"}
    disabled={busy}
    onClick={() => pinM.mutate()}
    class={`inline-flex h-6 w-6 items-center justify-center rounded-full transition-colors disabled:pointer-events-none disabled:opacity-50 ${
      item.pinned ? "bg-acc-soft text-acc" : "text-mut hover:bg-panel2 hover:text-ink"
    }`}
  >
    <svg viewBox="0 0 24 24" fill={item.pinned ? "currentColor" : "none"} stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="h-[14px] w-[14px]" aria-hidden="true">
      <path d="M12 17v5" />
      <path d="M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V7a1 1 0 0 1 1-1 2 2 0 0 0 0-4H8a2 2 0 0 0 0 4 1 1 0 0 1 1 1z" />
    </svg>
  </button>

  <!-- divider: sets the destructive action apart from the two toggles -->
  <span class="mx-[1px] h-3.5 w-px bg-line" aria-hidden="true"></span>

  <!-- Remove (destructive, one-shot) -->
  <button
    type="button"
    aria-label="Remove"
    title="Remove"
    disabled={busy}
    onClick={() => removeM.mutate()}
    class="inline-flex h-6 w-6 items-center justify-center rounded-full text-danger transition-colors hover:bg-danger/15 disabled:pointer-events-none disabled:opacity-50"
  >
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" class="h-[12px] w-[12px]" aria-hidden="true">
      <path d="M18 6 6 18M6 6l12 12" />
    </svg>
  </button>
</div>
```

- **Segmented, not three free-floating buttons.** One rounded-full container
  (`bg-panel/95` + `border-line` hairline + `shadow-sm` + `backdrop-blur-sm`)
  holding three 24px buttons reads as a *single* control and is ~30% narrower
  than three separately-plated pills — the deciding factor on 160px columns.
  It stays in the `WatchlistQuickActions` design family (translucent plate,
  backdrop-blur, group-reveal, pointer-events gating) without copying its
  poster-scrim, which has no meaning on a text card.
- **`bg-panel/95` (near-opaque), not `/90`.** The cluster floats over a
  tinted card wash (§2.5); a near-opaque plate keeps the glyphs reading
  against the neutral chip rather than the hue behind it, matching the time
  pill's own "sit on the tint, don't blend into it" reasoning (§3).
- **The divider (`w-px bg-line`) before Remove** groups the two reversible
  toggles and quarantines the irreversible action, lowering mis-click risk in
  a small hover target — the same "destructive actions get visual distance"
  instinct behind `ItemMenu` placing Remove last and `muted`.

### A.3 Reveal mechanics

- Hidden at rest: `opacity-0 pointer-events-none`. Revealed by
  `group-hover:opacity-100 group-hover:pointer-events-auto` and
  `group-focus-within:opacity-100 group-focus-within:pointer-events-auto` —
  the `group` is the §4.2 wrapper (already present in its class string), so no
  new plumbing.
- The reveal is **opacity only** (150ms) — no translate, scale, or size
  change. That keeps it out of the vestibular-motion category (so it needs no
  `prefers-reduced-motion` gate, §A.9) and keeps it visually independent of
  the wrapper's 2.4s glow pulse, which continues underneath on its own clock.
- `pointer-events-none` at rest means the invisible cluster can't be
  mouse-hit, but leaves the buttons in the tab order for keyboard reveal
  (§A.8) — identical to `WatchlistQuickActions`.
- **Suppressed while the `ItemMenu` is open** (`{!open && …}`): once the user
  has drilled in, the menu carries the same actions (plus Swap/Move/Details),
  so showing the cluster too would be duplicate, competing controls sitting on
  top of the expanded menu. Rendering-gate it off; the menu is the superset.

### A.4 Iconography (exact glyphs)

All three share `viewBox="0 0 24 24"`, `stroke="currentColor"`,
`stroke-linecap="round"`, `stroke-linejoin="round"`, `aria-hidden="true"`
(the accessible name lives on the button, §A.8). Per-glyph optical sizing so
they read as one weight despite different footprints.

| Action | Glyph | Path(s) | Stroke / fill | Rendered size |
|---|---|---|---|---|
| Watched | Check | `M20 6 9 17l-5-5` | `stroke-width 2.5`, `fill none` | `h-[13px] w-[13px]` |
| Pin | Pushpin (Lucide `pin`) | `M12 17v5` (stem) + `M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V7a1 1 0 0 1 1-1 2 2 0 0 0 0-4H8a2 2 0 0 0 0 4 1 1 0 0 1 1 1z` (head) | `stroke-width 2`; `fill none` when unpinned, `fill currentColor` when pinned | `h-[14px] w-[14px]` |
| Remove | X | `M18 6 6 18M6 6l12 12` | `stroke-width 2.5`, `fill none` | `h-[12px] w-[12px]` |

- **Pin outline↔filled is the load-bearing non-color cue.** Toggling the
  whole `<svg>`'s `fill` between `none` and `currentColor` swaps the pin head
  from hollow to solid; the stem sub-path (`M12 17v5`) is a zero-area line, so
  `fill` produces no artifact on it — it stays a stroked stem in both states.
  This means "pinned" is legible by *shape*, not color alone (§A.8).
- The X reads optically larger than the check/pin at equal px, so it is drawn
  one step smaller (`12px` vs `13/14px`) to keep the three visually balanced.

### A.5 The three actions — semantics & states

Each button reuses the **exact** mutation `ItemMenu` already runs, so a
shortcut and the menu are behaviorally identical (same endpoint, toast, and
cache invalidation). Implement them in a small `SlotQuickActions` component
that owns its own three `useMutation`s (the cluster is rendered when
`ItemMenu` is *not* mounted, so it can't borrow the menu's instances):

| Button | Mutation | Endpoint | Toggle? | Active/toggled treatment |
|---|---|---|---|---|
| Watched | `watchedM` | `POST /v1/guides/{gid}/items/{id}/watched` → invalidate `["shelf"]` + `["guide"]`, toast `Watched · {name}` | Yes — `aria-pressed={item.watched}` | `bg-acc-soft text-acc`; label flips to "Mark as not watched"; the whole card is also `opacity-50` at rest (§A.6) |
| Pin | `pinM` | `PATCH /v1/guides/{gid}/items/{id}` `{ pinned: !item.pinned }` → invalidate `["guide"]`, toast `Unpinned` / `Pinned to {dow}` | Yes — `aria-pressed={item.pinned}` | `bg-acc-soft text-acc` **+ filled pin glyph**; label flips to "Unpin"; the persistent "Pinned" body pill (§CalendarView) is unchanged |
| Remove | `removeM` | `DELETE /v1/guides/{gid}/items/{id}` → invalidate `["guide"]`, toast `Removed — enjoy the free hour` | No | `text-danger`, `hover:bg-danger/15`; no `aria-pressed`; card disappears on refetch |

- `busy = watchedM.isPending || pinM.isPending || removeM.isPending` disables
  all three during any pending mutation (`disabled:opacity-50
  disabled:pointer-events-none`), mirroring `ItemMenu`'s shared `busy` guard
  against double-fire.
- **Watched semantics caveat (for the implementer, t1-impl):** `ItemMenu`'s
  Watched chip today is *mark-only* (`POST …/watched`, no "unwatch"). The
  button here is specified as a full visual toggle — `aria-pressed` and the
  active treatment already model both directions — but a working
  *click-to-unwatch* depends on the mutation/endpoint supporting an off state.
  If it doesn't yet, that is an API/mutation change (out of scope for this
  visual spec); the button's states are already correct for the day it lands.
  Do **not** invent an unwatch endpoint to satisfy the toggle visuals.
- **Remove has no confirm dialog** — deliberately matching `ItemMenu`'s
  existing immediate Remove (toast-only). Adding a confirm to the shortcut but
  not the menu would be inconsistent; a confirm/undo pattern, if wanted, is a
  separate cross-surface decision (§A.10).

### A.6 Watched-card interaction (the un-dim)

A watched card carries `opacity-50` on the §4.2 wrapper. Because CSS opacity
composites the entire subtree, a child can **never** exceed its parent's
effective opacity — so a revealed cluster on a watched card would be stuck at
50%, which is a legibility (and, for the danger Remove, a contrast) problem.

Resolution: **the watched card un-dims to full opacity while hovered or
focused.** Refine the §4.2 wrapper's watched conditional and add `opacity` to
its transition list:

```diff
- transition-[box-shadow,transform] duration-200 ease-out
+ transition-[box-shadow,transform,opacity] duration-200 ease-out
  …
- {watched ? "opacity-50" : ""}
+ {watched ? "opacity-50 hover:opacity-100 focus-within:opacity-100" : ""}
```

- At rest the watched card still reads as receded (`opacity-50`) — the state
  that matters for scanning the week.
- On hover/focus it fades to full opacity over 200ms, taking the whole card
  (title, tint, and the child cluster) with it — so the actions you're
  reaching for are fully opaque and the title you're acting on is finally
  readable. This is the standard "dimmed item brightens when you engage it"
  pattern, and it's a genuine usability win beyond just fixing the opacity
  cap.
- **Documented trade-off vs §4.2:** during that hover the poster glow is
  full-intensity, not the half-intensity §4.2 describes for a watched card.
  Intentional — active interaction warrants full feedback, and the recede
  reading is preserved at rest. The §4.2 watched bullet carries a
  cross-reference to here.
- The un-dim is an opacity fade (no transform/scale) — not vestibular motion,
  so it is kept under `prefers-reduced-motion` (§A.9).

### A.7 Full state matrix

| State | Quick-action cluster |
|---|---|
| Default (no hover/focus) | Not shown — `opacity-0 pointer-events-none`; still in the DOM and tab order |
| Hover / focus-within (menu closed) | Fades in (150ms) at top-right; the three buttons become interactive |
| Watched card | Card un-dims to full opacity on hover/focus (§A.6); Watched button active (`bg-acc-soft text-acc`, `aria-pressed=true`, label "Mark as not watched") |
| Pinned card | Pin button active (`bg-acc-soft text-acc`, **filled** glyph, `aria-pressed=true`, label "Unpin"); the body "Pinned" pill is unchanged |
| Watched **and** pinned | Both toggles show their active treatment independently |
| `open` (ItemMenu expanded) | Cluster **not rendered** — the menu supersedes it and carries the same three actions plus Swap/Move/Details |
| Pending (any of the 3 mutations) | All three buttons `disabled:opacity-50 disabled:pointer-events-none` (shared `busy`, mirrors `ItemMenu`) |
| Board **plan** cell | **No cluster** — display-only, non-interactive (consistent with §4.3) |
| Board **alternate** cell (swappable or inert) | **No cluster** — the board has no per-item menu to shortcut; the swappable cell already owns a swap-on-click gesture |
| `prefers-reduced-motion: reduce` | Cluster still reveals (opacity fade only); §4.1 still disables the card pulse; static glow remains; watched un-dim still applies (opacity, not motion) |

### A.8 Accessibility contract

- **Accessible names.** Every icon button has an `aria-label` **and** a
  matching `title` (native tooltip). Toggle labels flip with state ("Pin" ↔
  "Unpin", "Mark as watched" ↔ "Mark as not watched"); Remove is a constant
  "Remove". Glyph `<svg>`s are `aria-hidden="true"` so the name isn't
  duplicated.
- **Toggle state.** `aria-pressed` on Watched and Pin exposes on/off to AT.
  Remove is a plain action — no `aria-pressed`.
- **State is never conveyed by color alone** (WCAG 1.4.1): the toggled state
  adds a *filled segment background* (`bg-acc-soft`) — a shape change — on top
  of the color shift; Pin additionally swaps outline→filled glyph; Watched is
  further reinforced by the whole-card dim at rest. AT gets `aria-pressed`
  regardless.
- **Hit area** is `h-6 w-6` = 24×24px per button, meeting the 24px minimum
  even though the glyph is smaller (padding is part of the target).
- **Keyboard.** Buttons stay in the tab order at rest (`opacity-0` does not
  remove them; `pointer-events-none` blocks only the pointer). Tabbing to the
  card's main button fires the wrapper's `:focus-within` → the cluster
  reveals; tabbing onward reaches Watched → Pin → Remove; Enter/Space
  activates (unaffected by `pointer-events`). DOM order is main button first,
  then the cluster, so keyboard flow is "open-the-card, then its shortcuts".
- **Focus indicator.** The global `button:focus-visible { outline: 2px solid
  var(--acc); outline-offset: 2px }` (globals.css) applies unchanged — the
  accent ring is the a11y contract and layers over the cluster's own paint on
  a separate layer.
- **Does not steal the card's click.** The cluster buttons are **siblings** of
  the main button, not nested inside it (nested buttons are invalid HTML
  anyway). Their clicks target themselves; the card's open-menu handler only
  fires on the main button. The cluster's `z-10` + `pointer-events-auto` (on
  reveal) captures pointer events over its whole footprint, so even a click on
  its padding is a no-op rather than falling through to open the menu. No
  `stopPropagation` needed.
- **Contrast.** Idle glyph `text-mut` on `bg-panel/95` clears the 3:1
  non-text floor; hover raises it to `text-ink`. Active `text-acc` on
  `bg-acc-soft`, and `text-danger`, both read comfortably on the near-opaque
  plate in either theme.

### A.9 Reduced motion

The cluster introduces **no new keyframes and no transform/scale** — its
reveal and the watched un-dim are opacity fades, which are not
vestibular-triggering motion, so they run unconditionally. Everything else is
inherited from §4.1: `prefers-reduced-motion: reduce` still removes the 2.4s
card pulse while the static hover glow (the duplicated 0%/100% shadow)
remains. Nothing in this addendum needs its own reduced-motion branch.

### A.10 Explicitly out of scope

- **Swap, Move, Details stay in `ItemMenu` only.** They are multi-step
  (Swap/Move open sub-pickers) or navigational (Details routes away) — wrong
  shape for a one-tap hover shortcut. Moving a slot to another space is
  deferred entirely, per the product owner.
- **Board surfaces get no cluster** (§A.7) — plan cells are display-only,
  alternates already have a swap gesture, and the board has no per-item menu
  to shortcut.
- **No confirm/undo on Remove** — matches `ItemMenu`'s current immediate
  Remove. A confirm or undo-toast pattern would be a cross-surface change
  (menu + cluster together) and is out of scope here.
- **No change to the `ItemMenu` chip styling** (§7) — the cluster is a
  parallel entry point to the same mutations, not a restyle of the menu.
