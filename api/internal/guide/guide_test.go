// api/internal/guide/guide_test.go
package guide

import (
	"math/rand"
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

func TestGenerateDoesNotMutateInput(t *testing.T) {
	providers := []int64{9, 1, 5}
	airDates := []AiredEpisode{{Season: 1, Episode: 2, Date: "2026-01-07"}, {Season: 1, Episode: 1, Date: "2026-01-05"}}
	seasons := map[int]int{1: 10}
	in := Input{Seed: 2, Days: week(evening(), evening()),
		Titles: []Title{{ID: 1, Kind: "series", Runtime: 60, Providers: providers, Airing: true,
			Pointer: Pointer{1, 1}, SeasonEpisodes: seasons, AirDates: airDates}}}
	Generate(in)
	if !reflect.DeepEqual(providers, []int64{9, 1, 5}) {
		t.Fatalf("caller Providers mutated: %v", providers)
	}
	if !reflect.DeepEqual(airDates, []AiredEpisode{{Season: 1, Episode: 2, Date: "2026-01-07"}, {Season: 1, Episode: 1, Date: "2026-01-05"}}) {
		t.Fatalf("caller AirDates mutated: %v", airDates)
	}
	if !reflect.DeepEqual(seasons, map[int]int{1: 10}) {
		t.Fatalf("caller SeasonEpisodes mutated: %v", seasons)
	}
}

// ---- Task 3 tests (final-review fixes) ----

// permInput is a mixed fixture (airing series, plain series, movies, a
// keep) sized to exercise sort-order sensitivity in every pass.
func permInput() Input {
	mk := func(id int64, rt int, provs ...int64) Title {
		return Title{ID: id, Kind: "series", Runtime: rt, Providers: provs,
			Pointer: Pointer{1, 1}, SeasonEpisodes: map[int]int{1: 8, 2: 8}}
	}
	air := mk(5, 60, 3, 1)
	air.Airing = true
	air.AirDates = []AiredEpisode{
		{1, 1, "2026-01-02"}, {1, 2, "2026-01-06"}, {1, 3, "2026-01-08"}, {1, 4, "2026-01-15"},
	}
	dates := []string{"2026-01-05", "2026-01-06", "2026-01-07", "2026-01-08", "2026-01-09", "2026-01-10", "2026-01-11"}
	days := make([]Day, 0, len(dates))
	for i, d := range dates {
		w := evening()
		if i == 3 {
			w = Window{} // disabled day mid-range
		}
		days = append(days, Day{Date: d, Window: w})
	}
	keep := []Item{
		{Date: "2026-01-06", StartMin: 19 * 60, EndMin: 20 * 60, TitleID: 2, Season: 1, Episode: 1, Provider: 2, IsPlan: true, Pinned: true},
	}
	return Input{Seed: 99, Days: days,
		Titles: []Title{mk(1, 45, 1), mk(2, 60, 2), mk(3, 30, 2, 1), air,
			{ID: 4, Kind: "movie", Runtime: 130, Providers: []int64{4, 1}},
			{ID: 6, Kind: "movie", Runtime: 90, Providers: []int64{2}}},
		Keep: keep}
}

// TestPermutationInvariance asserts the determinism contract: Generate
// sorts titles/providers/days internally, so shuffling caller-supplied
// order must never change the output. Shuffling uses a fixed-seed
// rand.Rand (not time-seeded), so the test itself stays deterministic.
func TestPermutationInvariance(t *testing.T) {
	base := Generate(permInput())
	r := rand.New(rand.NewSource(7))
	for trial := 0; trial < 200; trial++ {
		in := permInput()
		r.Shuffle(len(in.Titles), func(i, j int) { in.Titles[i], in.Titles[j] = in.Titles[j], in.Titles[i] })
		r.Shuffle(len(in.Days), func(i, j int) { in.Days[i], in.Days[j] = in.Days[j], in.Days[i] })
		for k := range in.Titles {
			p := in.Titles[k].Providers
			r.Shuffle(len(p), func(i, j int) { p[i], p[j] = p[j], p[i] })
			a := in.Titles[k].AirDates
			r.Shuffle(len(a), func(i, j int) { a[i], a[j] = a[j], a[i] })
		}
		got := Generate(in)
		if !reflect.DeepEqual(base, got) {
			t.Fatalf("trial %d: permuted input diverged\nbase %v\ngot  %v", trial, base, got)
		}
	}
}

// varietyInput builds two series over 4 days: series 1 is airing with E1
// and E3 aired before the range (catch-up, fill-schedulable anywhere) and
// E2 pinned to Fri 01-09. Series 2 is a plain series always eligible. The
// ground-truth variety penalty (-3 for airing on the previous day's plan,
// read from the previous dayState's series set rather than a scalar that
// out-of-chronological writes could clobber) must keep series 1 off Sat
// once it lands Fri, so series 2 wins that slot outright.
func varietyInput(seed int64) Input {
	a := Title{ID: 1, Kind: "series", Runtime: 60, Providers: []int64{1}, Airing: true,
		Pointer: Pointer{1, 1}, SeasonEpisodes: map[int]int{1: 10},
		AirDates: []AiredEpisode{{1, 1, "2026-01-05"}, {1, 2, "2026-01-09"}, {1, 3, "2026-01-06"}}}
	b := Title{ID: 2, Kind: "series", Runtime: 60, Providers: []int64{2},
		Pointer: Pointer{1, 1}, SeasonEpisodes: map[int]int{1: 10}}
	days := []Day{
		{Date: "2026-01-07", Window: Window{19 * 60, 20 * 60}},
		{Date: "2026-01-08", Window: Window{19 * 60, 21 * 60}},
		{Date: "2026-01-09", Window: Window{19 * 60, 20 * 60}},
		{Date: "2026-01-10", Window: Window{19 * 60, 20 * 60}},
	}
	return Input{Seed: seed, Days: days, Titles: []Title{a, b}}
}

// TestVarietyPenaltyConsecutiveNights is the regression for the old
// scalar per-title last-placement-date tracker: air-pins run before fill,
// so series 1's Fri pin was an out-of-chronological-order write that used
// to clobber that scalar back to Thu, letting series 1 tie for (and
// sometimes win) Sat. Sweeps the same 50 seeds the reviewer's probe swept.
func TestVarietyPenaltyConsecutiveNights(t *testing.T) {
	violations := 0
	var example []Item
	for seed := int64(0); seed < 50; seed++ {
		out := Generate(varietyInput(seed))
		onDay := map[string]map[int64]bool{}
		for _, it := range out {
			if !it.IsPlan {
				continue
			}
			if onDay[it.Date] == nil {
				onDay[it.Date] = map[int64]bool{}
			}
			onDay[it.Date][it.TitleID] = true
		}
		if onDay["2026-01-09"][1] && onDay["2026-01-10"][1] && !onDay["2026-01-10"][2] {
			violations++
			if example == nil {
				for _, it := range out {
					if it.IsPlan {
						example = append(example, it)
					}
				}
			}
		}
	}
	if violations > 0 {
		t.Fatalf("variety miss in %d/50 seeds: series 1 scheduled Fri(pin)+Sat though correct -3 makes series 2 win outright.\nexample plan: %+v", violations, example)
	}
}

// ---- Task 4 tests (airing back catalog is schedulable) ----

// TestAiringBackCatalogSchedulable is the regression for the airing-rule
// root cause: title_airings (the source of AirDates) only ever stores
// FUTURE episodes, so an airing show's already-aired back catalog has no
// air-map entry. Unknown air date must mean "already aired" (schedulable
// anywhere), not "not yet aired" (blocked) -- otherwise an airing show with
// any future-dated episode goes almost entirely unschedulable. A known
// future date must still wait for, and pin to, its air night.
func TestAiringBackCatalogSchedulable(t *testing.T) {
	tt := series(1, 60, 1)
	tt.Airing = true
	tt.AirDates = []AiredEpisode{
		{Season: 1, Episode: 4, Date: "2026-01-09"}, // known future date: must wait for/pin to it
	}
	in := Input{Seed: 3, Days: week(evening(), evening()), Titles: []Title{tt}}
	out := Generate(in)

	placed := map[epKey]Item{}
	for _, it := range plans(out) {
		if it.TitleID == 1 {
			placed[epKey{it.Season, it.Episode}] = it
		}
	}

	// Back catalog: S1E1-E3 sit before the pointer's first candidate and
	// have no air-map entry (already aired, date unknown). The fill pass
	// must place them, not skip the show entirely.
	for _, k := range []epKey{{1, 1}, {1, 2}, {1, 3}} {
		it, ok := placed[k]
		if !ok {
			t.Fatalf("back catalog episode %v not placed at all: %v", k, placed)
		}
		if it.Pinned {
			t.Fatalf("back catalog episode %v unexpectedly pinned: %+v", k, it)
		}
	}

	// S1E4 has a known future air date: must appear exactly once, pinned,
	// on that date -- never placed early by fill (existing pin-behavior
	// contract; must not regress).
	e4, ok := placed[epKey{1, 4}]
	if !ok {
		t.Fatalf("S1E4 (known future date) not placed: %v", placed)
	}
	if !e4.Pinned || e4.Date != "2026-01-09" {
		t.Fatalf("S1E4 = %+v, want Pinned on 2026-01-09", e4)
	}
}
