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
