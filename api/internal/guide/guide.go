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
	// Watched passes through keeps untouched, so issue #14 can round-trip
	// guide_items rows without re-joining; place() never sets it.
	Watched bool
}

// Input is everything Generate needs; callers hydrate it from the store.
type Input struct {
	Seed int64
	// Days must have unique Date values; duplicates collapse in the
	// internal per-date index (dayByDate), so only one survives.
	Days   []Day
	Titles []Title
	// Keep is expected to hold plan items (IsPlan true). Non-plan keeps
	// pass through verbatim but consume no capacity and are invisible to
	// the alternates cap.
	Keep []Item
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
		moviePlaced: map[int64]bool{},
	}

	e.titles = make([]Title, len(in.Titles))
	copy(e.titles, in.Titles)
	sort.Slice(e.titles, func(i, j int) bool { return e.titles[i].ID < e.titles[j].ID })
	for i := range e.titles {
		t := &e.titles[i]
		// Deep-copy before sorting: the header copy above shares backing
		// arrays with the caller's slices, and Generate must never mutate
		// its input (purity + concurrent-caller safety).
		p := make([]int64, len(t.Providers))
		copy(p, t.Providers)
		sort.Slice(p, func(a, b int) bool { return p[a] < p[b] })
		t.Providers = p
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
	sort.SliceStable(e.days, func(i, j int) bool { return e.days[i].date < e.days[j].date })

	e.passKeep(in.Keep)
	e.passAirPins()
	e.passMovies()
	e.passFill()
	e.passAlternates()

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

// schedulable applies the airing rule: airing titles schedule freely
// except episodes with a known future air date, which wait for (and
// air-pin to, see passAirPins) their air night. title_airings only ever
// INSERTS future episodes — rows persist after airing and hydration doesn't
// date-filter — so a missing entry means the episode was never known-future:
// back catalog.
//
// Trade-off: an episode that's within the season's known episode count but
// beyond TVMaze's published schedule (no air date yet, but not actually
// aired either) could slip into the guide early under this rule. Accepted:
// airings refresh daily, so it self-corrects once TVMaze publishes the
// date.
func (e *engine) schedulable(t *Title, k epKey, date string) bool {
	if !t.Airing {
		return true
	}
	d, ok := e.air[t.ID][k]
	return !ok || d <= date
}

// passFill greedily fills remaining capacity day by day using the fixed
// scoring weights (fairness -2/placement, variety -3, cohesion +2).
func (e *engine) passFill() {
	type cand struct {
		t *Title
		k epKey
	}
	for di, ds := range e.days {
		if !ds.enabled() {
			continue
		}
		for {
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
				if di > 0 && e.days[di-1].series[t.ID] {
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
