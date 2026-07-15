# Guide Generation Engine (issue #13) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `internal/guide.Generate(Input) []Item` — the deterministic greedy scheduler, pure (no store/clock/I/O), with the issue's nine acceptance tests.

**Architecture:** One package, one entry point. Generate runs the spec's passes in order over per-day mutable state; `math/rand` seeded from Input.Seed is consulted ONLY to pick among equal-score/equal-capacity ties; all other iteration is over sorted slices. Task 1 lands types + passes 1–4 (init, keep, air-pins, movies) green; Task 2 lands passes 5–8 (greedy fill, sequencing, alternates, sort) and the full test matrix.

**Tech Stack:** Go 1.25 stdlib only (`math/rand`, `sort`, `math`).

## Global Constraints

- Branch `feat/13-guide-engine` (off `main`), squash-merge. Commit per task.
- Pure package: `api/internal/guide` imports NOTHING beyond stdlib. No time.Now, no I/O.
- Determinism: rng for tie-breaks only; sorted iteration everywhere; same Input → deep-equal output.
- Scoring weights (issue-fixed): fairness `-2 ×` prior placements (keeps and air-pins count); variety `-3` if placed on the previous day in Days order; cohesion `+2` if any of the title's providers is already on tonight's plan.
- Hard constraints everywhere: item fits entirely in the day's window; ≤1 episode per series per day (alternates also excluded for titles already planned that day); `Airing` titles only schedule episodes whose air date is known and ≤ the day's date; a title needs ≥1 provider.
- Packing is back-to-back from `Window.StartMin`; keeps advance the pack position to `max(nextMin, keep.EndMin)`.
- Air-pinned items get `Pinned: true`. Movies place at most once (`Season/Episode = 0/0`). Alternates: ≤3 per plan slot, `IsPlan: false`, same Date/StartMin, runtime must fit from the slot start to window end, provider-diverse (providers ≠ plan item's provider first, then title-ID order), never advance pointers or consume capacity.
- Output sort: `(Date asc, StartMin asc, IsPlan desc — plan before alternates, TitleID asc)`.
- The nine test names from the issue, verbatim. Tests are table-driven and pure.
- TDD per task.

---

### Task 1: types, init, keep pass, air-date pinning, movie placement

**Files:**
- Create: `api/internal/guide/guide.go`
- Create: `api/internal/guide/guide_test.go`

**Interfaces:**
- Produces: all exported types (`Window`, `Day`, `Pointer`, `AiredEpisode`, `Title`, `Item`, `Input`) and `Generate` running passes 1–4 (series greedy fill arrives in Task 2 — a rotation of non-airing series yields no items yet, by design).

- [ ] **Step 1: Write the failing tests**

```go
// api/internal/guide/guide_test.go
package guide

import (
	"reflect"
	"testing"
)

// ---- fixture helpers ----

// week returns 7 consecutive days 2026-01-05 (Mon) .. 2026-01-11 (Sun) with
// the given weekday/weekend windows (minutes from midnight). Disabled days
// pass a zero Window.
func week(weekday, weekend Window) []Day {
	dates := []string{"2026-01-05", "2026-01-06", "2026-01-07", "2026-01-08", "2026-01-09", "2026-01-10", "2026-01-11"}
	days := make([]Day, 7)
	for i, d := range dates {
		w := weekday
		if i >= 5 {
			w = weekend
		}
		days[i] = Day{Date: d, Window: w}
	}
	return days
}

func evening() Window { return Window{StartMin: 19 * 60, EndMin: 23 * 60} } // 240 min

func series(id int64, runtime int, providers ...int64) Title {
	return Title{ID: id, Kind: "series", Name: "S", Runtime: runtime, Providers: providers,
		Pointer: Pointer{Season: 1, Episode: 1}, SeasonEpisodes: map[int]int{1: 10, 2: 10}}
}

func movie(id int64, runtime int, providers ...int64) Title {
	return Title{ID: id, Kind: "movie", Name: "M", Runtime: runtime, Providers: providers}
}

func plans(items []Item) []Item {
	out := []Item{}
	for _, it := range items {
		if it.IsPlan {
			out = append(out, it)
		}
	}
	return out
}

// ---- Task 1 tests ----

func TestRegeneratePreservesKeep(t *testing.T) {
	keep := Item{Date: "2026-01-06", StartMin: 19 * 60, EndMin: 20 * 60,
		TitleID: 7, Season: 1, Episode: 3, Provider: 1, IsPlan: true, Pinned: true}
	in := Input{
		Seed: 42,
		Days: week(evening(), evening()),
		Titles: []Title{{ID: 7, Kind: "series", Runtime: 60, Providers: []int64{1},
			Pointer: Pointer{1, 1}, SeasonEpisodes: map[int]int{1: 10}}},
		Keep: []Item{keep},
	}
	out := Generate(in)

	var found *Item
	for i := range out {
		if out[i].Pinned && out[i].TitleID == 7 && out[i].Date == "2026-01-06" {
			found = &out[i]
			break
		}
	}
	if found == nil || !reflect.DeepEqual(*found, keep) {
		t.Fatalf("keep not preserved verbatim: %+v", found)
	}
	// The kept episode is marked placed: nothing else may schedule S1E3 of title 7.
	for _, it := range plans(out) {
		if it.TitleID == 7 && it.Season == 1 && it.Episode == 3 && !it.Pinned {
			t.Fatalf("kept episode rescheduled: %+v", it)
		}
	}
}

func TestAirdatePin(t *testing.T) {
	tt := series(1, 60, 5)
	tt.Airing = true
	tt.AirDates = []AiredEpisode{
		{Season: 1, Episode: 1, Date: "2026-01-04"}, // before range: schedulable but not pinned
		{Season: 1, Episode: 2, Date: "2026-01-07"}, // in range: pinned to the 7th
		{Season: 1, Episode: 3, Date: "2026-02-01"}, // beyond range: not schedulable in range
	}
	tt.SeasonEpisodes = map[int]int{1: 3}
	in := Input{Seed: 1, Days: week(evening(), evening()), Titles: []Title{tt}}
	out := Generate(in)

	var pinDates []string
	for _, it := range plans(out) {
		if it.TitleID == 1 && it.Season == 1 && it.Episode == 2 {
			pinDates = append(pinDates, it.Date)
			if !it.Pinned {
				t.Fatalf("air-date item not Pinned: %+v", it)
			}
		}
		if it.Season == 1 && it.Episode == 3 {
			t.Fatalf("unaired-in-range episode scheduled: %+v", it)
		}
	}
	if len(pinDates) != 1 || pinDates[0] != "2026-01-07" {
		t.Fatalf("S1E2 pin dates = %v, want exactly [2026-01-07]", pinDates)
	}
}

func TestMovieOnLongestDay(t *testing.T) {
	// Weekdays 120-minute windows, weekend 240 — the 130-minute movie only
	// fits (and belongs) on a weekend day.
	in := Input{
		Seed:   9,
		Days:   week(Window{19 * 60, 21 * 60}, Window{19 * 60, 23 * 60}),
		Titles: []Title{movie(3, 130, 2)},
	}
	out := Generate(in)
	got := plans(out)
	if len(got) != 1 {
		t.Fatalf("movie placements = %d, want 1", len(got))
	}
	m := got[0]
	if m.Date != "2026-01-10" && m.Date != "2026-01-11" {
		t.Fatalf("movie on %s, want a weekend day", m.Date)
	}
	if m.Season != 0 || m.Episode != 0 || m.Provider != 2 {
		t.Fatalf("movie item = %+v", m)
	}
	if m.EndMin-m.StartMin != 130 || m.EndMin > 23*60 {
		t.Fatalf("movie window violation: %+v", m)
	}
}
```

- [ ] **Step 2: Run to verify failure** — `cd api && go test ./internal/guide/` → FAIL (no package).

- [ ] **Step 3: Implement**

```go
// api/internal/guide/guide.go
//
// Package guide is the deterministic greedy scheduler: pure, no store or
// clock access. Generate's rand source (Input.Seed) is consulted for
// tie-breaks only; all other iteration is over sorted data, so identical
// inputs always produce identical guides. Pass order and scoring weights
// are fixed by issue #13 and the design spec.
package guide

import (
	"math/rand"
	"sort"
)

// Window is a viewing window in minutes from midnight. The zero Window
// marks a disabled day.
type Window struct {
	StartMin int
	EndMin   int
}

// Day is one calendar day of the guide range. Date is "YYYY-MM-DD", so
// lexicographic order is chronological.
type Day struct {
	Date   string
	Window Window
}

// Pointer is a user's position in a series.
type Pointer struct {
	Season  int
	Episode int
}

// AiredEpisode is a real air date for one episode of an airing series.
type AiredEpisode struct {
	Season  int
	Episode int
	Date    string
}

// Title is a fully hydrated rotation entry.
type Title struct {
	ID             int64
	Kind           string // "movie" | "series"
	Name           string
	Runtime        int // minutes
	Providers      []int64
	Airing         bool
	Pointer        Pointer
	SeasonEpisodes map[int]int
	AirDates       []AiredEpisode
}

// Item is one scheduled guide entry. Season/Episode 0/0 marks a movie;
// IsPlan false marks an alternate sharing a plan slot.
type Item struct {
	Date     string
	StartMin int
	EndMin   int
	TitleID  int64
	Season   int
	Episode  int
	Provider int64
	IsPlan   bool
	Pinned   bool
	Edited   bool
}

// Input is everything Generate needs; callers hydrate it from the store.
type Input struct {
	Seed   int64
	Days   []Day
	Titles []Title
	Keep   []Item
}

type epKey struct{ season, episode int }

type dayState struct {
	date      string
	window    Window
	nextMin   int
	series    map[int64]bool
	providers map[int64]bool
}

func (ds *dayState) remaining() int { return ds.window.EndMin - ds.nextMin }
func (ds *dayState) enabled() bool  { return ds.window != Window{} }

// engine carries Generate's working state so passes stay small.
type engine struct {
	rng         *rand.Rand
	titles      []Title
	byID        map[int64]*Title
	air         map[int64]map[epKey]string
	days        []*dayState
	dayByDate   map[string]*dayState
	placedEps   map[int64]map[epKey]bool
	placedCount map[int64]int
	lastPlaced  map[int64]string
	moviePlaced map[int64]bool
	out         []Item
}

// Generate produces the guide. See package comment for the determinism
// contract and the design spec for pass semantics.
func Generate(in Input) []Item {
	e := &engine{
		rng:         rand.New(rand.NewSource(in.Seed)),
		byID:        map[int64]*Title{},
		air:         map[int64]map[epKey]string{},
		dayByDate:   map[string]*dayState{},
		placedEps:   map[int64]map[epKey]bool{},
		placedCount: map[int64]int{},
		lastPlaced:  map[int64]string{},
		moviePlaced: map[int64]bool{},
	}

	e.titles = make([]Title, len(in.Titles))
	copy(e.titles, in.Titles)
	sort.Slice(e.titles, func(i, j int) bool { return e.titles[i].ID < e.titles[j].ID })
	for i := range e.titles {
		t := &e.titles[i]
		sort.Slice(t.Providers, func(a, b int) bool { return t.Providers[a] < t.Providers[b] })
		e.byID[t.ID] = t
		m := map[epKey]string{}
		for _, a := range t.AirDates {
			m[epKey{a.Season, a.Episode}] = a.Date
		}
		e.air[t.ID] = m
	}

	for _, d := range in.Days {
		ds := &dayState{date: d.Date, window: d.Window, nextMin: d.Window.StartMin,
			series: map[int64]bool{}, providers: map[int64]bool{}}
		e.days = append(e.days, ds)
		e.dayByDate[d.Date] = ds
	}

	e.passKeep(in.Keep)
	e.passAirPins()
	e.passMovies()

	sort.Slice(e.out, func(i, j int) bool {
		a, b := e.out[i], e.out[j]
		if a.Date != b.Date {
			return a.Date < b.Date
		}
		if a.StartMin != b.StartMin {
			return a.StartMin < b.StartMin
		}
		if a.IsPlan != b.IsPlan {
			return a.IsPlan
		}
		return a.TitleID < b.TitleID
	})
	return e.out
}

func (e *engine) markPlaced(titleID int64, k epKey) {
	if e.placedEps[titleID] == nil {
		e.placedEps[titleID] = map[epKey]bool{}
	}
	e.placedEps[titleID][k] = true
}

// place appends a plan item and updates all state.
func (e *engine) place(ds *dayState, t *Title, season, episode int, provider int64, pinned bool) {
	e.out = append(e.out, Item{Date: ds.date, StartMin: ds.nextMin, EndMin: ds.nextMin + t.Runtime,
		TitleID: t.ID, Season: season, Episode: episode, Provider: provider, IsPlan: true, Pinned: pinned})
	ds.nextMin += t.Runtime
	ds.series[t.ID] = true
	ds.providers[provider] = true
	if t.Kind == "series" {
		e.markPlaced(t.ID, epKey{season, episode})
	} else {
		e.moviePlaced[t.ID] = true
	}
	e.placedCount[t.ID]++
	e.lastPlaced[t.ID] = ds.date
}

// pickProvider prefers a provider already on tonight's plan, else the
// lowest id. Providers are pre-sorted; caller guarantees non-empty.
func (e *engine) pickProvider(t *Title, ds *dayState) int64 {
	for _, p := range t.Providers {
		if ds.providers[p] {
			return p
		}
	}
	return t.Providers[0]
}

// passKeep copies pinned/edited/watched items through verbatim, consuming
// capacity and claiming their episodes so later passes skip them.
func (e *engine) passKeep(keep []Item) {
	ks := make([]Item, len(keep))
	copy(ks, keep)
	sort.Slice(ks, func(i, j int) bool {
		if ks[i].Date != ks[j].Date {
			return ks[i].Date < ks[j].Date
		}
		return ks[i].StartMin < ks[j].StartMin
	})
	for _, k := range ks {
		e.out = append(e.out, k)
		if !k.IsPlan {
			continue
		}
		if ds := e.dayByDate[k.Date]; ds != nil {
			ds.series[k.TitleID] = true
			ds.providers[k.Provider] = true
			if k.EndMin > ds.nextMin {
				ds.nextMin = k.EndMin
			}
		}
		if k.Season > 0 {
			e.markPlaced(k.TitleID, epKey{k.Season, k.Episode})
		} else if t := e.byID[k.TitleID]; t != nil && t.Kind == "movie" {
			e.moviePlaced[k.TitleID] = true
		}
		e.placedCount[k.TitleID]++
		if prev, ok := e.lastPlaced[k.TitleID]; !ok || k.Date > prev {
			e.lastPlaced[k.TitleID] = k.Date
		}
	}
}

// passAirPins pins in-range aired episodes of airing series to their air
// nights (appointment TV).
func (e *engine) passAirPins() {
	for i := range e.titles {
		t := &e.titles[i]
		if t.Kind != "series" || !t.Airing || len(t.Providers) == 0 {
			continue
		}
		aired := make([]AiredEpisode, len(t.AirDates))
		copy(aired, t.AirDates)
		sort.Slice(aired, func(a, b int) bool {
			if aired[a].Season != aired[b].Season {
				return aired[a].Season < aired[b].Season
			}
			return aired[a].Episode < aired[b].Episode
		})
		for _, a := range aired {
			if a.Season < t.Pointer.Season ||
				(a.Season == t.Pointer.Season && a.Episode < t.Pointer.Episode) {
				continue
			}
			k := epKey{a.Season, a.Episode}
			if e.placedEps[t.ID][k] {
				continue
			}
			ds := e.dayByDate[a.Date]
			if ds == nil || !ds.enabled() || ds.series[t.ID] || ds.remaining() < t.Runtime {
				continue
			}
			e.place(ds, t, a.Season, a.Episode, e.pickProvider(t, ds), true)
		}
	}
}

// passMovies places each movie once, on the day with the most remaining
// capacity that fits it (ties broken by rng).
func (e *engine) passMovies() {
	for i := range e.titles {
		t := &e.titles[i]
		if t.Kind != "movie" || e.moviePlaced[t.ID] || len(t.Providers) == 0 {
			continue
		}
		best := -1
		var cands []*dayState
		for _, ds := range e.days {
			if !ds.enabled() {
				continue
			}
			rem := ds.remaining()
			if rem < t.Runtime {
				continue
			}
			switch {
			case rem > best:
				best, cands = rem, []*dayState{ds}
			case rem == best:
				cands = append(cands, ds)
			}
		}
		if len(cands) == 0 {
			continue
		}
		ds := cands[e.rng.Intn(len(cands))]
		e.place(ds, t, 0, 0, e.pickProvider(t, ds), false)
	}
}
```

- [ ] **Step 4: Run to verify pass** — `cd api && go test ./internal/guide/ -v && go vet ./...` → 3 tests PASS.
- [ ] **Step 5: Commit**

```bash
git add api/internal/guide/
git commit -m "feat(api): guide engine types, keep/air-pin/movie passes"
```

### Task 2: greedy fill, pointer sequencing, alternates, output sort

**Files:**
- Modify: `api/internal/guide/guide.go` (add passFill, passAlternates, nextEpisode, schedulable; call them from Generate between passMovies and the final sort)
- Modify: `api/internal/guide/guide_test.go` (add the remaining six tests)

**Interfaces:**
- Consumes: Task 1's engine internals. Public surface unchanged (Generate now fills and appends alternates).

- [ ] **Step 1: Write the failing tests** (append)

```go
// ---- Task 2 tests ----

func TestDeterministic(t *testing.T) {
	mk := func() Input {
		s1, s2, s3 := series(1, 60, 1), series(2, 60, 2), series(3, 30, 1, 2)
		m := movie(4, 120, 3)
		return Input{Seed: 1234, Days: week(evening(), evening()), Titles: []Title{s1, s2, s3, m}}
	}
	a, b := Generate(mk()), Generate(mk())
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("same input diverged:\n%v\n%v", a, b)
	}
	if len(plans(a)) == 0 {
		t.Fatal("no plan items generated")
	}
}

func TestFitsWindow(t *testing.T) {
	in := Input{Seed: 5, Days: week(Window{20 * 60, 22 * 60}, Window{18 * 60, 23 * 60}),
		Titles: []Title{series(1, 45, 1), series(2, 60, 2), movie(3, 200, 1)}}
	byDate := map[string]Window{}
	for _, d := range in.Days {
		byDate[d.Date] = d.Window
	}
	for _, it := range Generate(in) {
		w := byDate[it.Date]
		if it.StartMin < w.StartMin || it.EndMin > w.EndMin {
			t.Fatalf("item outside window %+v (window %+v)", it, w)
		}
		if it.EndMin-it.StartMin <= 0 {
			t.Fatalf("non-positive duration: %+v", it)
		}
	}
}

func TestOneEpisodePerSeriesPerDay(t *testing.T) {
	// One 30-min series, plenty of window: without the constraint it would
	// fill a whole evening with the same show.
	in := Input{Seed: 3, Days: week(evening(), evening()), Titles: []Title{series(1, 30, 1)}}
	seen := map[string]int{}
	for _, it := range plans(Generate(in)) {
		seen[it.Date]++
		if seen[it.Date] > 1 {
			t.Fatalf("series scheduled twice on %s", it.Date)
		}
	}
	if len(seen) != 7 {
		t.Fatalf("series on %d days, want 7 (fairness/fill)", len(seen))
	}
}

func TestFairness(t *testing.T) {
	// Three 60-min series, 240-min windows: 21 slots over the week vs 28
	// needed for 4/day — every title must appear, spread within 1.
	in := Input{Seed: 7, Days: week(evening(), evening()),
		Titles: []Title{series(1, 60, 1), series(2, 60, 2), series(3, 60, 3)}}
	counts := map[int64]int{}
	for _, it := range plans(Generate(in)) {
		counts[it.TitleID]++
	}
	if len(counts) != 3 {
		t.Fatalf("titles placed = %d, want all 3 (counts %v)", len(counts), counts)
	}
	min, max := 1<<30, 0
	for _, c := range counts {
		if c < min {
			min = c
		}
		if c > max {
			max = c
		}
	}
	if max-min > 1 {
		t.Fatalf("fairness spread = %v", counts)
	}
}

func TestPointerSequence(t *testing.T) {
	// Pointer starts at S1E9 with 10 eps in S1: placements must run
	// S1E9, S1E10, S2E1, S2E2, ... in date order.
	tt := series(1, 60, 1)
	tt.Pointer = Pointer{Season: 1, Episode: 9}
	in := Input{Seed: 11, Days: week(evening(), evening()), Titles: []Title{tt}}
	var got []epKey
	for _, it := range plans(Generate(in)) {
		got = append(got, epKey{it.Season, it.Episode})
	}
	want := []epKey{{1, 9}, {1, 10}, {2, 1}, {2, 2}, {2, 3}, {2, 4}, {2, 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("episode sequence = %v, want %v", got, want)
	}
}

func TestAlternates(t *testing.T) {
	// Five titles across distinct providers: plan slots must carry up to 3
	// alternates, provider-diverse first, never a title already planned
	// that day, and pointers must not advance from alternate listing.
	titles := []Title{series(1, 60, 1), series(2, 60, 2), series(3, 60, 3), series(4, 60, 4), series(5, 60, 5)}
	in := Input{Seed: 21, Days: week(evening(), evening()), Titles: titles}
	out := Generate(in)

	plansBySlot := map[[2]interface{}]Item{}
	altsBySlot := map[[2]interface{}][]Item{}
	planned := map[[2]interface{}]map[int64]bool{}
	for _, it := range out {
		key := [2]interface{}{it.Date, it.StartMin}
		if it.IsPlan {
			plansBySlot[key] = it
			continue
		}
		altsBySlot[key] = append(altsBySlot[key], it)
	}
	for _, it := range plans(out) {
		if planned[[2]interface{}{it.Date, 0}] == nil {
			planned[[2]interface{}{it.Date, 0}] = map[int64]bool{}
		}
		planned[[2]interface{}{it.Date, 0}][it.TitleID] = true
	}

	if len(altsBySlot) == 0 {
		t.Fatal("no alternates generated")
	}
	for key, alts := range altsBySlot {
		p, ok := plansBySlot[key]
		if !ok {
			t.Fatalf("alternates without plan at %v", key)
		}
		if len(alts) > 3 {
			t.Fatalf("%d alternates at %v, want <=3", len(alts), key)
		}
		for _, a := range alts {
			if a.TitleID == p.TitleID {
				t.Fatalf("alternate repeats plan title at %v", key)
			}
			if planned[[2]interface{}{a.Date, 0}][a.TitleID] {
				t.Fatalf("alternate %d already planned on %s", a.TitleID, a.Date)
			}
			if a.StartMin != p.StartMin {
				t.Fatalf("alternate start %d != plan start %d", a.StartMin, p.StartMin)
			}
		}
		// Provider diversity: with 5 single-provider titles, at least one
		// alternate must be on a provider different from the plan's.
		diverse := false
		for _, a := range alts {
			if a.Provider != p.Provider {
				diverse = true
			}
		}
		if !diverse {
			t.Fatalf("no provider-diverse alternate at %v", key)
		}
	}

	// Alternates never advance pointers: rerunning with the same input is
	// deep-equal (covered by TestDeterministic) AND every plan episode for
	// each title is still the exact pointer sequence.
	seq := map[int64][]epKey{}
	for _, it := range plans(out) {
		seq[it.TitleID] = append(seq[it.TitleID], epKey{it.Season, it.Episode})
	}
	for id, eps := range seq {
		for i, k := range eps {
			want := epKey{1, i + 1}
			if k != want {
				t.Fatalf("title %d plan episode %d = %v, want %v (alternates leaked into pointer state)", id, i, k, want)
			}
		}
	}
}
```

- [ ] **Step 2: Run to verify failure** — fill absent: TestDeterministic fails on "no plan items", others fail similarly.

- [ ] **Step 3: Implement** — add to guide.go, and in `Generate` insert `e.passFill()` then `e.passAlternates()` between `e.passMovies()` and the final sort:

```go
// nextEpisode returns t's next unplaced episode from its pointer, wrapping
// seasons via SeasonEpisodes; ok=false when exhausted. Never mutates state.
func (e *engine) nextEpisode(t *Title) (epKey, bool) {
	s, ep := t.Pointer.Season, t.Pointer.Episode
	if s < 1 {
		s = 1
	}
	if ep < 1 {
		ep = 1
	}
	for {
		cnt, ok := t.SeasonEpisodes[s]
		if !ok {
			return epKey{}, false
		}
		if ep > cnt {
			s, ep = s+1, 1
			continue
		}
		k := epKey{s, ep}
		if !e.placedEps[t.ID][k] {
			return k, true
		}
		ep++
	}
}

// schedulable applies the airing rule: airing titles only schedule
// episodes whose air date is known and on/before the target date.
func (e *engine) schedulable(t *Title, k epKey, date string) bool {
	if !t.Airing {
		return true
	}
	d, ok := e.air[t.ID][k]
	return ok && d <= date
}

// passFill greedily fills remaining capacity day by day using the fixed
// scoring weights (fairness -2/placement, variety -3, cohesion +2).
func (e *engine) passFill() {
	for di, ds := range e.days {
		if !ds.enabled() {
			continue
		}
		prevDate := ""
		if di > 0 {
			prevDate = e.days[di-1].date
		}
		for {
			type cand struct {
				t *Title
				k epKey
			}
			best := -1 << 30
			var ties []cand
			for i := range e.titles {
				t := &e.titles[i]
				if t.Kind != "series" || len(t.Providers) == 0 || ds.series[t.ID] || ds.remaining() < t.Runtime {
					continue
				}
				k, ok := e.nextEpisode(t)
				if !ok || !e.schedulable(t, k, ds.date) {
					continue
				}
				score := -2 * e.placedCount[t.ID]
				if prevDate != "" && e.lastPlaced[t.ID] == prevDate {
					score -= 3
				}
				for _, p := range t.Providers {
					if ds.providers[p] {
						score += 2
						break
					}
				}
				switch {
				case score > best:
					best, ties = score, []cand{{t, k}}
				case score == best:
					ties = append(ties, cand{t, k})
				}
			}
			if len(ties) == 0 {
				break
			}
			c := ties[e.rng.Intn(len(ties))]
			e.place(ds, c.t, c.k.season, c.k.episode, e.pickProvider(c.t, ds), false)
		}
	}
}

// passAlternates appends up to 3 provider-diverse alternates per plan slot.
// Reads pointer state without mutating it.
func (e *engine) passAlternates() {
	planIdx := make([]int, 0, len(e.out))
	for i := range e.out {
		if e.out[i].IsPlan {
			planIdx = append(planIdx, i)
		}
	}
	sort.Slice(planIdx, func(a, b int) bool {
		x, y := e.out[planIdx[a]], e.out[planIdx[b]]
		if x.Date != y.Date {
			return x.Date < y.Date
		}
		return x.StartMin < y.StartMin
	})

	var alts []Item
	for _, pi := range planIdx {
		p := e.out[pi]
		ds := e.dayByDate[p.Date]
		if ds == nil {
			continue
		}
		limit := ds.window.EndMin - p.StartMin
		var prefer, rest []Item
		for i := range e.titles {
			t := &e.titles[i]
			if t.ID == p.TitleID || len(t.Providers) == 0 || t.Runtime > limit || ds.series[t.ID] {
				continue
			}
			season, episode := 0, 0
			if t.Kind == "series" {
				k, ok := e.nextEpisode(t)
				if !ok || !e.schedulable(t, k, p.Date) {
					continue
				}
				season, episode = k.season, k.episode
			} else if e.moviePlaced[t.ID] {
				continue
			}
			prov := t.Providers[0]
			for _, pr := range t.Providers {
				if pr != p.Provider {
					prov = pr
					break
				}
			}
			alt := Item{Date: p.Date, StartMin: p.StartMin, EndMin: p.StartMin + t.Runtime,
				TitleID: t.ID, Season: season, Episode: episode, Provider: prov, IsPlan: false}
			if prov != p.Provider {
				prefer = append(prefer, alt)
			} else {
				rest = append(rest, alt)
			}
		}
		slot := append(prefer, rest...)
		if len(slot) > 3 {
			slot = slot[:3]
		}
		alts = append(alts, slot...)
	}
	e.out = append(e.out, alts...)
}
```

- [ ] **Step 4: Run everything** — `cd api && go test ./internal/guide/ -v -count=2 && go vet ./... && go test ./...` → all nine tests PASS twice (count=2 guards hidden nondeterminism).
- [ ] **Step 5: Commit**

```bash
git add api/internal/guide/
git commit -m "feat(api): greedy fill, pointer sequencing, alternates"
```

### Task 3: PR (controller-inline)

- [ ] **Step 1:** Clean-state `go vet ./... && go test ./...`; run the guide suite with `-race -count=5`.
- [ ] **Step 2:** Push; PR `feat(api): guide generation engine` — closes #13; notes the provenance reconstruction (original plan lost; rebuilt from issue + design spec, decisions marked in the spec doc).
- [ ] **Step 3:** CI green → user squash-merges.

---

## Self-review notes

- All nine acceptance test names present (T1: 3, T2: 6), each testing its named property against the spec's semantics.
- Determinism guards: sorted titles/providers/aired/keeps/planIdx; rng only in movie-day ties and fill-score ties; `-count=2` in T2 and `-race -count=5` at PR time.
- passAlternates reads `nextEpisode` AFTER all fill placements, so an alternate suggests the episode the user would actually watch next; it never calls place/markPlaced (pointer state untouched) — asserted by TestAlternates' sequence check.
- TestAlternates' `ds.series[t.ID]` exclusion enforces the ≤1-per-series-per-day constraint across plan+alternate swaps.
