package store

import (
	"context"
	"testing"
	"time"
)

func int64p(v int64) *int64 { return &v }

// uniqueTMDBID returns a per-test-run unique fake TMDB id so tests never
// collide on the (kind, tmdb_id) unique key across runs.
func uniqueTMDBID() int64 { return time.Now().UnixNano() }

func TestUpsertTitleInsertAndRefresh(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tmdbID := uniqueTMDBID()

	ti, err := s.UpsertTitle(ctx, TitleUpsert{
		TMDBID: tmdbID, Kind: "series", TVMazeID: int64p(82),
		Name: "Upsert Show", Overview: "o", PosterPath: "/p.jpg",
		RuntimeMinutes: 60, Airing: true,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if ti.ID == 0 || ti.TMDBID != tmdbID || ti.Kind != "series" || ti.TVMazeID == nil || *ti.TVMazeID != 82 ||
		ti.Name != "Upsert Show" || ti.RuntimeMinutes != 60 || !ti.Airing {
		t.Fatalf("insert title = %+v", ti)
	}
	if time.Since(ti.RefreshedAt) > time.Minute {
		t.Fatalf("refreshed_at not stamped on insert: %v", ti.RefreshedAt)
	}
	// Provider/airing stamps default to epoch = immediately stale.
	if ti.ProvidersRefreshedAt.Year() != 1970 || ti.AiringsRefreshedAt.Year() != 1970 {
		t.Fatalf("class stamps not epoch: %v / %v", ti.ProvidersRefreshedAt, ti.AiringsRefreshedAt)
	}

	// Refresh without TVMazeID: metadata updates, tvmaze_id survives
	// (COALESCE), refreshed_at re-stamps, other stamps untouched.
	first := ti.RefreshedAt
	ti2, err := s.UpsertTitle(ctx, TitleUpsert{
		TMDBID: tmdbID, Kind: "series",
		Name: "Upsert Show S2", Overview: "o2", PosterPath: "/p2.jpg",
		RuntimeMinutes: 55, Airing: false,
	})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if ti2.ID != ti.ID || ti2.Name != "Upsert Show S2" || ti2.RuntimeMinutes != 55 || ti2.Airing {
		t.Fatalf("refresh title = %+v", ti2)
	}
	if ti2.TVMazeID == nil || *ti2.TVMazeID != 82 {
		t.Fatalf("tvmaze_id clobbered on refresh: %v", ti2.TVMazeID)
	}
	if ti2.RefreshedAt.Before(first) {
		t.Fatalf("refreshed_at not re-stamped: %v < %v", ti2.RefreshedAt, first)
	}
}

func TestGetTitleByTMDBAbsent(t *testing.T) {
	s := testStore(t)
	ti, err := s.GetTitleByTMDB(context.Background(), "movie", uniqueTMDBID())
	if err != nil || ti != nil {
		t.Fatalf("absent title = %+v, err %v; want nil, nil", ti, err)
	}
}

func TestReplaceSeasons(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ti, err := s.UpsertTitle(ctx, TitleUpsert{TMDBID: uniqueTMDBID(), Kind: "series", Name: "Seasons"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.ReplaceSeasons(ctx, ti.ID, []SeasonRow{{Number: 1, EpisodeCount: 10}, {Number: 2, EpisodeCount: 8}}); err != nil {
		t.Fatalf("replace 2: %v", err)
	}
	if err := s.ReplaceSeasons(ctx, ti.ID, []SeasonRow{{Number: 1, EpisodeCount: 11}}); err != nil {
		t.Fatalf("replace 1: %v", err)
	}
	full, err := s.GetTitleFull(ctx, seedUser(t, s), ti.ID, "US")
	if err != nil {
		t.Fatalf("full: %v", err)
	}
	if len(full.Seasons) != 1 || full.Seasons[0].Number != 1 || full.Seasons[0].EpisodeCount != 11 {
		t.Fatalf("seasons after replace = %+v", full.Seasons)
	}
}

func TestReplaceProvidersRegionScoped(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := seedUser(t, s)
	ti, err := s.UpsertTitle(ctx, TitleUpsert{TMDBID: uniqueTMDBID(), Kind: "movie", Name: "Providers"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	before := ti.ProvidersRefreshedAt

	if err := s.ReplaceProviders(ctx, ti.ID, "US", []ProviderRow{{ID: 8, Name: "Netflix", LogoPath: "/n.jpg"}, {ID: 9, Name: "Max", LogoPath: "/m.jpg"}}); err != nil {
		t.Fatalf("US: %v", err)
	}
	if err := s.ReplaceProviders(ctx, ti.ID, "GB", []ProviderRow{{ID: 9, Name: "Max", LogoPath: "/m.jpg"}}); err != nil {
		t.Fatalf("GB: %v", err)
	}
	// Re-replace US; GB must be untouched, dictionary row updated.
	if err := s.ReplaceProviders(ctx, ti.ID, "US", []ProviderRow{{ID: 8, Name: "Netflix Renamed", LogoPath: "/n2.jpg"}}); err != nil {
		t.Fatalf("US re-replace: %v", err)
	}

	us, err := s.GetTitleFull(ctx, uid, ti.ID, "US")
	if err != nil {
		t.Fatalf("full US: %v", err)
	}
	if len(us.Providers) != 1 || us.Providers[0].ID != 8 || us.Providers[0].Name != "Netflix Renamed" || us.Providers[0].LogoPath != "/n2.jpg" {
		t.Fatalf("US providers = %+v", us.Providers)
	}
	gb, err := s.GetTitleFull(ctx, uid, ti.ID, "GB")
	if err != nil {
		t.Fatalf("full GB: %v", err)
	}
	if len(gb.Providers) != 1 || gb.Providers[0].ID != 9 {
		t.Fatalf("GB providers = %+v", gb.Providers)
	}
	if !us.Title.ProvidersRefreshedAt.After(before) {
		t.Fatalf("providers_refreshed_at not stamped: %v", us.Title.ProvidersRefreshedAt)
	}
}

func TestReplaceFutureAirings(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := seedUser(t, s)
	ti, err := s.UpsertTitle(ctx, TitleUpsert{TMDBID: uniqueTMDBID(), Kind: "series", Name: "Airings"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	before := ti.AiringsRefreshedAt

	// Two future rows relative to a fixed "today" far in the future so the
	// test never rots.
	if err := s.ReplaceFutureAirings(ctx, ti.ID, "2100-01-01", []AiringRow{
		{Season: 1, Episode: 1, AirDate: "2100-01-02"},
		{Season: 1, Episode: 2, AirDate: "2100-01-03"},
	}); err != nil {
		t.Fatalf("first replace: %v", err)
	}
	// Later "today": S1E1 (2100-01-02) is now past and must survive the
	// delete; S1E2 (>= today) is deleted; S1E3 inserted.
	if err := s.ReplaceFutureAirings(ctx, ti.ID, "2100-01-03", []AiringRow{
		{Season: 1, Episode: 3, AirDate: "2100-01-04"},
	}); err != nil {
		t.Fatalf("second replace: %v", err)
	}
	rows, err := s.Pool.Query(ctx,
		`SELECT season, episode, to_char(air_date, 'YYYY-MM-DD') FROM title_airings WHERE title_id = $1 ORDER BY season, episode`, ti.ID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	type air struct {
		s, e int
		d    string
	}
	var got []air
	for rows.Next() {
		var a air
		if err := rows.Scan(&a.s, &a.e, &a.d); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, a)
	}
	want := []air{{1, 1, "2100-01-02"}, {1, 3, "2100-01-04"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("airings = %+v, want %+v", got, want)
	}

	// A rescheduled episode (same PK, new future date) upserts, not dupes.
	if err := s.ReplaceFutureAirings(ctx, ti.ID, "2100-01-01", []AiringRow{
		{Season: 1, Episode: 1, AirDate: "2100-02-01"},
	}); err != nil {
		t.Fatalf("reschedule: %v", err)
	}
	var d string
	if err := s.Pool.QueryRow(ctx,
		`SELECT to_char(air_date, 'YYYY-MM-DD') FROM title_airings WHERE title_id = $1 AND season = 1 AND episode = 1`, ti.ID).Scan(&d); err != nil {
		t.Fatalf("reschedule query: %v", err)
	}
	if d != "2100-02-01" {
		t.Fatalf("rescheduled air_date = %s, want 2100-02-01", d)
	}

	full, err := s.GetTitleFull(ctx, uid, ti.ID, "US")
	if err != nil {
		t.Fatalf("full: %v", err)
	}
	if !full.Title.AiringsRefreshedAt.After(before) {
		t.Fatalf("airings_refreshed_at not stamped")
	}
}

func TestGetTitleFullEntryAndEmptySlices(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := seedUser(t, s)
	ti, err := s.UpsertTitle(ctx, TitleUpsert{TMDBID: uniqueTMDBID(), Kind: "movie", Name: "Full Movie"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	full, err := s.GetTitleFull(ctx, uid, ti.ID, "US")
	if err != nil {
		t.Fatalf("full (no entry): %v", err)
	}
	if full.Title.ID != ti.ID || full.Title.Name != "Full Movie" {
		t.Fatalf("title = %+v", full.Title)
	}
	if full.Entry != nil {
		t.Fatalf("entry = %+v, want nil before any user interaction", full.Entry)
	}
	if full.Seasons == nil || len(full.Seasons) != 0 || full.Providers == nil || len(full.Providers) != 0 {
		t.Fatalf("empty slices must be non-nil: seasons=%v providers=%v", full.Seasons, full.Providers)
	}

	if _, err := s.UpsertEntry(ctx, uid, ti.ID, EntryUpdate{Status: strp("watchlist")}); err != nil {
		t.Fatalf("entry: %v", err)
	}
	full, err = s.GetTitleFull(ctx, uid, ti.ID, "US")
	if err != nil {
		t.Fatalf("full (entry): %v", err)
	}
	if full.Entry == nil || full.Entry.Status != "watchlist" || full.Entry.TitleID != ti.ID {
		t.Fatalf("entry = %+v", full.Entry)
	}

	if _, err := s.GetTitleFull(ctx, uid, -1, "US"); err == nil {
		t.Fatal("unknown title id: want error")
	}
}
