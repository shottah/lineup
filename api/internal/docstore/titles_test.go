package docstore

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func int64p(v int64) *int64 { return &v }

// uniqueTMDBID returns a per-test-run unique fake TMDB id.
func uniqueTMDBID() int64 { return time.Now().UnixNano() }

func mustTitleDoc(t *testing.T, s *Store, id string) *titleDoc {
	t.Helper()
	m, err := s.getTitles(context.Background(), []string{id})
	if err != nil {
		t.Fatalf("getTitles: %v", err)
	}
	d := m[id]
	if d == nil {
		t.Fatalf("title %s missing", id)
	}
	return d
}

func TestTitleID(t *testing.T) {
	if got := TitleID("series", 1399); got != "series-1399" {
		t.Fatalf("TitleID = %q", got)
	}
}

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
	if ti.ID != TitleID("series", tmdbID) || ti.TMDBID != tmdbID || ti.Kind != "series" || ti.TVMazeID == nil || *ti.TVMazeID != 82 ||
		ti.Name != "Upsert Show" || ti.RuntimeMinutes != 60 || !ti.Airing {
		t.Fatalf("insert title = %+v", ti)
	}
	if time.Since(ti.RefreshedAt) > time.Minute {
		t.Fatalf("refreshedAt not stamped on insert: %v", ti.RefreshedAt)
	}
	if ti.ProvidersRefreshedAt.UTC().Year() != 1970 || ti.AiringsRefreshedAt.UTC().Year() != 1970 {
		t.Fatalf("class stamps not epoch: %v / %v", ti.ProvidersRefreshedAt, ti.AiringsRefreshedAt)
	}

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
		t.Fatalf("tvmazeId clobbered on refresh: %v", ti2.TVMazeID)
	}
	if ti2.RefreshedAt.Before(first) {
		t.Fatalf("refreshedAt not re-stamped: %v < %v", ti2.RefreshedAt, first)
	}

	// Refresh preserves provider/airing stamps and data.
	if err := s.ReplaceProviders(ctx, ti.ID, "US", []ProviderRow{{ID: 8, Name: "N"}}); err != nil {
		t.Fatalf("providers: %v", err)
	}
	if err := s.ReplaceFutureAirings(ctx, ti.ID, "2100-01-01", []AiringRow{{Season: 1, Episode: 1, AirDate: "2100-01-02"}}); err != nil {
		t.Fatalf("airings: %v", err)
	}
	before := mustTitleDoc(t, s, ti.ID)
	if _, err := s.UpsertTitle(ctx, TitleUpsert{TMDBID: tmdbID, Kind: "series", Name: "Again"}); err != nil {
		t.Fatalf("third upsert: %v", err)
	}
	after := mustTitleDoc(t, s, ti.ID)
	if !after.ProvidersRefreshedAt.Equal(before.ProvidersRefreshedAt) || !after.AiringsRefreshedAt.Equal(before.AiringsRefreshedAt) ||
		len(after.Airings) != 1 || !reflect.DeepEqual(after.Providers, before.Providers) {
		t.Fatalf("upsert clobbered class data: before %+v after %+v", before, after)
	}
}

func TestGetTitleByTMDBAbsent(t *testing.T) {
	s := testStore(t)
	ti, err := s.GetTitleByTMDB(context.Background(), "movie", uniqueTMDBID())
	if err != nil || ti != nil {
		t.Fatalf("absent title = %+v, err %v; want nil, nil", ti, err)
	}
}

func TestGetTitleByTMDBPresent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	id := uniqueTMDBID()
	if _, err := s.UpsertTitle(ctx, TitleUpsert{TMDBID: id, Kind: "movie", Name: "M"}); err != nil {
		t.Fatal(err)
	}
	ti, err := s.GetTitleByTMDB(ctx, "movie", id)
	if err != nil || ti == nil || ti.Name != "M" || ti.ID != TitleID("movie", id) {
		t.Fatalf("got %+v, %v", ti, err)
	}
}

func TestGetTitlesOnlyFound(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	id := uniqueTMDBID()
	if _, err := s.UpsertTitle(ctx, TitleUpsert{TMDBID: id, Kind: "movie", Name: "M"}); err != nil {
		t.Fatal(err)
	}
	have := TitleID("movie", id)
	m, err := s.getTitles(ctx, []string{have, "movie-0", "bad/id", ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || m[have] == nil {
		t.Fatalf("getTitles = %v", m)
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
	if got := mustTitleDoc(t, s, ti.ID).Seasons; !reflect.DeepEqual(got, map[string]int{"1": 11}) {
		t.Fatalf("seasons after replace = %v", got)
	}
	if err := s.ReplaceSeasons(ctx, ti.ID, []SeasonRow{{Number: 3, EpisodeCount: 8}, {Number: 3, EpisodeCount: 9}}); err != nil {
		t.Fatalf("duplicate season numbers: %v", err)
	}
	if got := mustTitleDoc(t, s, ti.ID).Seasons; !reflect.DeepEqual(got, map[string]int{"3": 9}) {
		t.Fatalf("seasons after dup replace = %v, want only 3:9", got)
	}
}

func TestReplaceOnMissingTitleErrors(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	id := TitleID("movie", uniqueTMDBID())
	if err := s.ReplaceSeasons(ctx, id, nil); err == nil {
		t.Fatal("seasons: want error")
	}
	if err := s.ReplaceProviders(ctx, id, "US", nil); err == nil {
		t.Fatal("providers: want error")
	}
	if err := s.ReplaceFutureAirings(ctx, id, "2100-01-01", nil); err == nil {
		t.Fatal("airings: want error")
	}
	if m, _ := s.getTitles(ctx, []string{id}); len(m) != 0 {
		t.Fatalf("title was created: %v", m)
	}
}

func TestReplaceProvidersRegionScoped(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ti, err := s.UpsertTitle(ctx, TitleUpsert{TMDBID: uniqueTMDBID(), Kind: "movie", Name: "Providers"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	before := ti.ProvidersRefreshedAt

	if err := s.ReplaceProviders(ctx, ti.ID, "US", []ProviderRow{{ID: 9, Name: "Max", LogoPath: "/m.jpg"}, {ID: 8, Name: "Netflix", LogoPath: "/n.jpg"}}); err != nil {
		t.Fatalf("US: %v", err)
	}
	if got := mustTitleDoc(t, s, ti.ID).Providers["US"]; !reflect.DeepEqual(got, []int64{8, 9}) {
		t.Fatalf("US not sorted ascending: %v", got)
	}
	if err := s.ReplaceProviders(ctx, ti.ID, "GB", []ProviderRow{{ID: 9, Name: "Max", LogoPath: "/m.jpg"}}); err != nil {
		t.Fatalf("GB: %v", err)
	}
	if err := s.ReplaceProviders(ctx, ti.ID, "US", []ProviderRow{{ID: 8, Name: "Netflix Renamed", LogoPath: "/n2.jpg"}}); err != nil {
		t.Fatalf("US re-replace: %v", err)
	}
	d := mustTitleDoc(t, s, ti.ID)
	if !reflect.DeepEqual(d.Providers["US"], []int64{8}) || !reflect.DeepEqual(d.Providers["GB"], []int64{9}) {
		t.Fatalf("providers = %v", d.Providers)
	}
	if !d.ProvidersRefreshedAt.After(before) {
		t.Fatalf("providersRefreshedAt not stamped: %v", d.ProvidersRefreshedAt)
	}
	snap, err := s.c.Doc("providers/8").Get(ctx)
	if err != nil {
		t.Fatalf("provider doc: %v", err)
	}
	var p ProviderRow
	if err := snap.DataTo(&p); err != nil || p.Name != "Netflix Renamed" || p.LogoPath != "/n2.jpg" {
		t.Fatalf("provider doc = %+v, %v", p, err)
	}

	if err := s.ReplaceProviders(ctx, ti.ID, "US", []ProviderRow{{ID: 10, Name: "Dup"}, {ID: 10, Name: "Dup"}}); err != nil {
		t.Fatalf("duplicate provider ids: %v", err)
	}
	if got := mustTitleDoc(t, s, ti.ID).Providers["US"]; !reflect.DeepEqual(got, []int64{10}) {
		t.Fatalf("US after dup = %v", got)
	}
}

func TestReplaceProvidersKeepsProviderDictionary(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ti, err := s.UpsertTitle(ctx, TitleUpsert{TMDBID: uniqueTMDBID(), Kind: "movie", Name: "Dict"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceProviders(ctx, ti.ID, "US", []ProviderRow{{ID: 500, Name: "P500", LogoPath: "/p.jpg"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceProviders(ctx, ti.ID, "US", []ProviderRow{}); err != nil {
		t.Fatal(err)
	}
	snap, err := s.c.Doc("providers/500").Get(ctx)
	if err != nil {
		t.Fatalf("providers/500: %v", err)
	}
	name, _ := snap.DataAt("name")
	if name != "P500" {
		t.Fatalf("name = %v", name)
	}
	if got := mustTitleDoc(t, s, ti.ID).Providers["US"]; len(got) != 0 {
		t.Fatalf("US = %v, want empty", got)
	}
}

func TestReplaceFutureAirings(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ti, err := s.UpsertTitle(ctx, TitleUpsert{TMDBID: uniqueTMDBID(), Kind: "series", Name: "Airings"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	before := ti.AiringsRefreshedAt

	if err := s.ReplaceFutureAirings(ctx, ti.ID, "2100-01-01", []AiringRow{
		{Season: 1, Episode: 1, AirDate: "2100-01-02"},
		{Season: 1, Episode: 2, AirDate: "2100-01-03"},
	}); err != nil {
		t.Fatalf("first replace: %v", err)
	}
	if err := s.ReplaceFutureAirings(ctx, ti.ID, "2100-01-03", []AiringRow{
		{Season: 1, Episode: 3, AirDate: "2100-01-04"},
	}); err != nil {
		t.Fatalf("second replace: %v", err)
	}
	d := mustTitleDoc(t, s, ti.ID)
	want := []airing{{1, 1, "2100-01-02"}, {1, 3, "2100-01-04"}}
	if !reflect.DeepEqual(d.Airings, want) {
		t.Fatalf("airings = %+v, want %+v", d.Airings, want)
	}

	// A rescheduled episode upserts, not dupes.
	if err := s.ReplaceFutureAirings(ctx, ti.ID, "2100-01-01", []AiringRow{
		{Season: 1, Episode: 1, AirDate: "2100-02-01"},
	}); err != nil {
		t.Fatalf("reschedule: %v", err)
	}
	d = mustTitleDoc(t, s, ti.ID)
	if !reflect.DeepEqual(d.Airings, []airing{{1, 1, "2100-02-01"}}) {
		t.Fatalf("airings after reschedule = %+v", d.Airings)
	}
	if !d.AiringsRefreshedAt.After(before) {
		t.Fatalf("airingsRefreshedAt not stamped")
	}
}
