package docstore

import (
	"context"
	"testing"

	"cloud.google.com/go/firestore"

	"github.com/shottah/lineup/api/internal/guide"
)

// TestReplaceProvidersRecordsOffer: the region's offer type is stored next
// to its provider ids and surfaces on GetTitleByTMDB (ingest staleness) and
// GetTitleFull (title page label). A region with no offers still records
// its key, so ingest can tell "fetched, nothing there" from "never fetched".
func TestReplaceProvidersRecordsOffer(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tmdbID := uniqueTMDBID()
	ti, err := s.UpsertTitle(ctx, TitleUpsert{TMDBID: tmdbID, Kind: "movie", Name: "Hero"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetTitleByTMDB(ctx, "movie", tmdbID); len(got.ProviderOffers) != 0 {
		t.Fatalf("fresh title offers = %v, want none recorded", got.ProviderOffers)
	}
	if err := s.ReplaceProviders(ctx, ti.ID, "US", guide.OfferRent, []ProviderRow{{ID: 10, Name: "Amazon Video"}, {ID: 2, Name: "Apple TV"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceProviders(ctx, ti.ID, "GB", "", []ProviderRow{}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTitleByTMDB(ctx, "movie", tmdbID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProviderOffers["US"] != guide.OfferRent {
		t.Fatalf("US offer = %q, want rent", got.ProviderOffers["US"])
	}
	if v, ok := got.ProviderOffers["GB"]; !ok || v != "" {
		t.Fatalf("GB offer = %q (recorded %v), want recorded empty", v, ok)
	}
	full, err := s.GetTitleFull(ctx, "", ti.ID, "US")
	if err != nil {
		t.Fatal(err)
	}
	if full.Offer != guide.OfferRent || len(full.Providers) != 2 {
		t.Fatalf("title full = offer %q providers %+v, want rent with 2 providers", full.Offer, full.Providers)
	}
}

// TestLegacyFlatrateDocsReadAsStream: title docs and guide items written
// before offers existed held flatrate data only, so a missing offer reads
// as "stream" wherever providers exist.
func TestLegacyFlatrateDocsReadAsStream(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := seedUser(t, s)
	id := seedTitle(t, s, "Legacy Series")
	// Pre-offer shape: providers map only, no providerOffers.
	if _, err := s.c.Collection("titles").Doc(id).Update(ctx, []firestore.Update{
		{Path: "providers", Value: map[string][]int64{"US": {8}}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertEntry(ctx, uid, id, EntryUpdate{Status: strp("rotation")}); err != nil {
		t.Fatal(err)
	}
	titles, err := s.GuideInputTitles(ctx, uid, "US")
	if err != nil || len(titles) != 1 || titles[0].Offer != guide.OfferStream {
		t.Fatalf("legacy hydration = %+v, %v; want one title with offer stream", titles, err)
	}
	full, err := s.GetTitleFull(ctx, uid, id, "US")
	if err != nil || full.Offer != guide.OfferStream {
		t.Fatalf("legacy title full offer = %+v, %v; want stream", full, err)
	}
	info, err := s.SwapTitle(ctx, uid, id, "US")
	if err != nil || info.Offer != guide.OfferStream {
		t.Fatalf("legacy swap info = %+v, %v; want offer stream", info, err)
	}

	// A guide item stored without an offer reads back as stream.
	g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: id, Season: 1, Episode: 1, Provider: 8, IsPlan: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if g.Items[0].Offer != guide.OfferStream {
		t.Fatalf("legacy item offer = %q, want stream", g.Items[0].Offer)
	}
}

// TestSwapCarriesOffer: swapping a slot to a rent-only title relabels the
// item as a rental along with its provider.
func TestSwapCarriesOffer(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, movieID := seedGuideWorld(t, s) // movie is rent-only on US:902
	g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, Offer: guide.OfferStream, IsPlan: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.SwapTitle(ctx, uid, movieID, "US")
	if err != nil || info.Offer != guide.OfferRent {
		t.Fatalf("swap info = %+v, %v; want offer rent", info, err)
	}
	sz, ez := 0, 0
	it, err := s.UpdateGuideItem(ctx, uid, g.ID, g.Items[0].ID, GuideItemUpdate{
		TitleID: &movieID, Season: &sz, Episode: &ez, SwapProviders: info.Providers, SwapOffer: info.Offer, SetEdited: true,
	})
	if err != nil || it.ProviderID != 902 || it.Offer != guide.OfferRent {
		t.Fatalf("swapped item = %+v, %v; want provider 902 offer rent", it, err)
	}
	back, err := s.GuideWithItems(ctx, uid, g.ID)
	if err != nil || back.Items[0].Offer != guide.OfferRent {
		t.Fatalf("persisted item = %+v, %v; want offer rent", back, err)
	}
}
