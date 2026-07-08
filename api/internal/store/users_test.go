package store

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// testStore opens the TEST_DATABASE_URL database, running migrations first.
// Tests are skipped when TEST_DATABASE_URL is unset so `go test ./...`
// stays hermetic (CI has no Postgres).
func testStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping store integration test")
	}
	if err := Migrate(url); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := New(ctx, url)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func uniqueUID(t *testing.T) string {
	t.Helper()
	return "test-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + time.Now().Format("150405.000000000")
}

func TestUpsertUserByFirebaseUID(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := uniqueUID(t)
	defaults := json.RawMessage(`{"windows":{}}`)

	u1, err := s.UpsertUserByFirebaseUID(ctx, uid, "a@example.com", "Ada", defaults)
	if err != nil {
		t.Fatalf("insert upsert: %v", err)
	}
	if u1.ID == 0 || u1.Email != "a@example.com" || u1.DisplayName != "Ada" || u1.Region != "US" {
		t.Fatalf("insert upsert = %+v, want fresh row with defaults", u1)
	}
	if string(u1.SchedulePrefs) != `{"windows": {}}` && string(u1.SchedulePrefs) != `{"windows":{}}` {
		t.Fatalf("insert prefs = %s, want defaults applied", u1.SchedulePrefs)
	}

	// Second upsert: same uid, changed email, empty display name (must keep old).
	conflictDefaults := json.RawMessage(`{"windows":{"changed":true}}`)
	u2, err := s.UpsertUserByFirebaseUID(ctx, uid, "b@example.com", "", conflictDefaults)
	if err != nil {
		t.Fatalf("update upsert: %v", err)
	}
	if u2.ID != u1.ID {
		t.Fatalf("update upsert created new row: id %d != %d", u2.ID, u1.ID)
	}
	if u2.Email != "b@example.com" || u2.DisplayName != "Ada" {
		t.Fatalf("update upsert = %+v, want email updated, display name kept", u2)
	}
	if strings.Contains(string(u2.SchedulePrefs), "changed") {
		t.Fatalf("conflict update overwrote schedule_prefs: %s", u2.SchedulePrefs)
	}

	// Empty email on a later token must not blank the stored email.
	u3, err := s.UpsertUserByFirebaseUID(ctx, uid, "", "Ada2", conflictDefaults)
	if err != nil {
		t.Fatalf("empty-email upsert: %v", err)
	}
	if u3.Email != "b@example.com" || u3.DisplayName != "Ada2" {
		t.Fatalf("empty-email upsert = %+v, want email kept, display name updated", u3)
	}
}

func TestUpdateUserPrefs(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, err := s.UpsertUserByFirebaseUID(ctx, uniqueUID(t), "c@example.com", "Cy", json.RawMessage(`{"windows":{}}`))
	if err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	region := "GB"
	newPrefs := json.RawMessage(`{"windows":{"mon":{"enabled":false,"start":"09:00","end":"10:00"}}}`)
	u2, err := s.UpdateUserPrefs(ctx, u.ID, &region, newPrefs)
	if err != nil {
		t.Fatalf("update both: %v", err)
	}
	if u2.Region != "GB" || !strings.Contains(string(u2.SchedulePrefs), `"09:00"`) {
		t.Fatalf("update both = %+v", u2)
	}

	// nil region and nil prefs leave fields untouched.
	u3, err := s.UpdateUserPrefs(ctx, u.ID, nil, nil)
	if err != nil {
		t.Fatalf("no-op update: %v", err)
	}
	if u3.Region != "GB" || !strings.Contains(string(u3.SchedulePrefs), `"09:00"`) {
		t.Fatalf("no-op update changed row: %+v", u3)
	}

	// region-only update leaves prefs untouched.
	region2 := "US"
	u4, err := s.UpdateUserPrefs(ctx, u.ID, &region2, nil)
	if err != nil {
		t.Fatalf("region-only update: %v", err)
	}
	if u4.Region != "US" || !strings.Contains(string(u4.SchedulePrefs), `"09:00"`) {
		t.Fatalf("region-only update wrong: %+v", u4)
	}

	// prefs-only update leaves region untouched.
	prefs2 := json.RawMessage(`{"windows":{"tue":{"enabled":true,"start":"20:00","end":"21:00"}}}`)
	u5, err := s.UpdateUserPrefs(ctx, u.ID, nil, prefs2)
	if err != nil {
		t.Fatalf("prefs-only update: %v", err)
	}
	if u5.Region != "US" || !strings.Contains(string(u5.SchedulePrefs), `"20:00"`) {
		t.Fatalf("prefs-only update wrong: %+v", u5)
	}
}
