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
	in := Input{Seed: 2, Days: week(evening(), evening()),
		Titles: []Title{{ID: 1, Kind: "series", Runtime: 60, Providers: providers,
			Pointer: Pointer{1, 1}, SeasonEpisodes: map[int]int{1: 10}}}}
	Generate(in)
	if !reflect.DeepEqual(providers, []int64{9, 1, 5}) {
		t.Fatalf("caller Providers mutated: %v", providers)
	}
}
