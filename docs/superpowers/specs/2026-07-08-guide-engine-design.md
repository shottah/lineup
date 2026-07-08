# Guide generation engine (issue #13) — design

Date: 2026-07-08
Status: approved (user: "proceed", after reconstructed-design presentation)
Issue: [#13 feat(api): guide generation engine](https://github.com/shottah/lineup/issues/13)

## Provenance note

The original v1 plan's "task 13" (exact types + 8-step algorithm) was never
committed and is lost with the early sessions. This spec reconstructs the
engine from the two surviving sources — the issue body (pass order, scoring
weights, determinism rules, the nine acceptance test names) and the v1
design spec's Guide Generation Engine section (hard/soft constraints,
alternates) — and fills the type-level gaps to match the `guide_items`
schema. Re-derived decisions are marked (†).

Final-review follow-ups (2026-07-08):

- v1's alternates drew candidates from rotation *and* watchlist titles on
  other services. This engine's `Input` only carries the plan-eligible
  pool, so alternates can fall back to a same-provider title but cannot
  express "watchlist-only, not in rotation" candidates — that requires
  hydrating a separate pool, deferred to issue #14's store hydration work.
- v1's "longest-window days" rule for movie placement is implemented here
  as "greatest remaining capacity" rather than raw window length. This is
  a deliberate refinement (†): a long day that's already loaded up with
  keeps/pins shouldn't out-rank a shorter day that's still wide open.

## Shape

`api/internal/guide` — pure package: no store imports, no clock, no I/O,
no globals. `Generate(Input) []Item`. Determinism contract: `math/rand`
seeded from `Input.Seed` is consulted for TIE-BREAKS ONLY; every other
iteration is over sorted keys/slices. Same Input → identical output, byte
for byte.

## Types (†, aligned with guide_items: start_min/end_min ints, season=0 = movie)

```go
type Window struct{ StartMin, EndMin int }             // minutes from midnight
type Day struct{ Date string; Window Window }          // "YYYY-MM-DD"; zero Window = disabled day
type Pointer struct{ Season, Episode int }
type AiredEpisode struct{ Season, Episode int; Date string }
type Title struct {
    ID             int64
    Kind           string          // "movie" | "series"
    Name           string
    Runtime        int             // minutes; movie runtime / typical ep runtime
    Providers      []int64         // providers carrying it in the user's region
    Airing         bool
    Pointer        Pointer         // series progression start
    SeasonEpisodes map[int]int     // season -> episode count; drives wrapping
    AirDates       []AiredEpisode  // airing series only
}
type Item struct {
    Date     string
    StartMin, EndMin int
    TitleID  int64
    Season, Episode int              // 0/0 for movies
    Provider int64
    IsPlan   bool                    // false = alternate
    Pinned, Edited bool
}
type Input struct{ Seed int64; Days []Day; Titles []Title; Keep []Item }
```

## Passes (issue-fixed order; scoring weights issue-fixed)

1. **Init** — rng from seed; titles sorted by ID; per-day state: next free
   minute (packing is back-to-back from window start †), series-on-day set,
   providers-on-day set. Disabled days have no capacity.
2. **Keep-items** — `Keep` (user-pinned/edited/watched items on regenerate)
   pass through verbatim, consuming capacity †, marking series-per-day, and
   marking their episodes placed so sequencing skips them.
3. **Air-date pinning** — for each airing series (sorted ID, then
   season/episode): every not-yet-placed episode at-or-after the pointer
   whose air date falls on an in-range, enabled day is placed on exactly
   that day (constraints permitting: fits remaining window, series not
   already on that day). Air-pinned items get `Pinned: true` † (appointment
   TV survives regeneration semantically; regeneration also reproduces them
   deterministically).
4. **Movies to longest days** — movies sorted by ID; each placed once † on
   the day with the greatest remaining capacity that fits its runtime
   (ties → rng pick among tied days).
5. **Greedy fill** — days in order; while capacity remains, score every
   eligible series: `-2 × placements so far` (fairness; keeps and pins
   count †) `- 3 if placed on the previous day` (variety)
   `+ 2 if one of its providers is already on tonight's plan` (cohesion).
   Eligible = has a next episode schedulable today (airing rule below),
   runtime fits remaining window, not already on this day, has ≥1 provider
   (hard constraint 4). Highest score wins; ties → rng among tied
   candidates (iterated in title-ID order). Provider choice: a provider
   already on tonight if any (lowest id), else lowest id †.
6. **Pointer sequencing** — successive placements of a series consume
   successive episodes from `Pointer`, skipping already-placed ones,
   wrapping seasons via `SeasonEpisodes`; a series with no remaining
   episodes is exhausted. **Airing rule:** for `Airing` titles, an episode
   is schedulable on date d only if its air date is known and ≤ d; unknown
   air date = not yet aired = not schedulable †. Non-airing titles are
   unrestricted.
7. **Alternates** — each plan slot gets up to 3 alternates (`IsPlan:
   false`, same Date/StartMin): other titles whose next episode/runtime
   fits from the slot's start to the window end †, schedulable that day
   (airing rule), provider-diverse — candidates on a provider different
   from the plan item's come first †, then by title ID. Alternates consume
   no capacity and never advance pointers.
8. **Output** — sorted by `(Date, StartMin, IsPlan desc, TitleID)`.

## Hard constraints (v1 spec, enforced throughout)

Fits entirely within the day's window; ≤1 episode per series per day;
airing episodes never before their air date (air-date episodes pinned to
their air night); title only on a provider that carries it.

## Testing

Table-driven, pure, the issue's nine names exactly: TestDeterministic
(same input twice → deep-equal; different seed may differ),
TestFitsWindow, TestOneEpisodePerSeriesPerDay, TestFairness (ample
capacity → every title ≥1×, counts spread ≤1 apart †), TestAirdatePin,
TestMovieOnLongestDay, TestRegeneratePreservesKeep, TestAlternates (≤3,
provider-diverse, no pointer movement), TestPointerSequence (successive
episodes, season wrap). Acceptance: `go test ./internal/guide/` green.

## Out of scope

Guide persistence and endpoints (#14), hydration from store (#14),
schedule-prefs parsing (callers translate prefs → `[]Day`), watched-marking
pointer advancement (#14), gap-aware packing.
