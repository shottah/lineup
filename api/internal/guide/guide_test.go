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
