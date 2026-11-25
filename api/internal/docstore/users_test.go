package docstore

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUpsertUserByFirebaseUID(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := uniqueUID(t)
	defaults := json.RawMessage(`{"windows":{}}`)

	u1, err := s.UpsertUserByFirebaseUID(ctx, uid, "a@example.com", "Ada", defaults)
	if err != nil {
		t.Fatalf("insert upsert: %v", err)
	}
	if u1.ID != uid || u1.Email != "a@example.com" || u1.DisplayName != "Ada" || u1.Region != "US" {
		t.Fatalf("insert upsert = %+v, want fresh doc with defaults", u1)
	}
	if string(u1.SchedulePrefs) != `{"windows":{}}` {
		t.Fatalf("insert prefs = %s, want defaults applied", u1.SchedulePrefs)
	}
	b, err := json.Marshal(u1)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"id":"`+uid+`"`) {
		t.Fatalf("json = %s, want id %q", b, uid)
	}

	// Second upsert: same uid, changed email, empty display name (must keep old).
	conflictDefaults := json.RawMessage(`{"windows":{"changed":true}}`)
	u2, err := s.UpsertUserByFirebaseUID(ctx, uid, "b@example.com", "", conflictDefaults)
	if err != nil {
		t.Fatalf("update upsert: %v", err)
	}
	if u2.ID != u1.ID {
		t.Fatalf("update upsert created new doc: id %s != %s", u2.ID, u1.ID)
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

	// Refresh keeps region and prefs that were changed in between.
	region := "GB"
	if _, err := s.UpdateUserPrefs(ctx, uid, &region, json.RawMessage(`{"windows":{"mon":{}}}`)); err != nil {
		t.Fatalf("update prefs: %v", err)
	}
	u4, err := s.UpsertUserByFirebaseUID(ctx, uid, "c@example.com", "Ada3", defaults)
	if err != nil {
		t.Fatalf("refresh upsert: %v", err)
	}
	if u4.Email != "c@example.com" || u4.DisplayName != "Ada3" || u4.Region != "GB" || !strings.Contains(string(u4.SchedulePrefs), "mon") {
		t.Fatalf("refresh upsert = %+v, want email/name refreshed, region/prefs kept", u4)
	}
}

func TestUpsertUserEmptyDefaultPrefs(t *testing.T) {
	s := testStore(t)
	u, err := s.UpsertUserByFirebaseUID(context.Background(), uniqueUID(t), "e@example.com", "E", nil)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if string(u.SchedulePrefs) != `{}` {
		t.Fatalf("prefs = %s, want {}", u.SchedulePrefs)
	}
}

func TestUpsertUserInvalidUID(t *testing.T) {
	s := testStore(t)
	if _, err := s.UpsertUserByFirebaseUID(context.Background(), "a/b", "", "", nil); err == nil {
		t.Fatal("want error for invalid uid")
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
		t.Fatalf("no-op update changed doc: %+v", u3)
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

func TestUpdateUserPrefsMissingUser(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	region := "GB"
	if _, err := s.UpdateUserPrefs(ctx, "no-such-user", &region, nil); err == nil {
		t.Fatal("want error for missing user")
	}
	// Must not have created the doc.
	snap, err := s.c.Collection("users").Doc("no-such-user").Get(ctx)
	if err == nil && snap.Exists() {
		t.Fatal("missing user doc was created")
	}
}

func TestUpsertUserByFirebaseUIDConcurrentFirstLogin(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := uniqueUID(t)
	defaults := json.RawMessage(`{"windows":{}}`)

	const n = 10
	users := make([]*User, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			users[i], errs[i] = s.UpsertUserByFirebaseUID(ctx, uid, "a@example.com", "Ada", defaults)
		}(i)
	}
	close(start)
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("upsert %d: %v", i, errs[i])
		}
		if users[i].ID != uid || users[i].Email != "a@example.com" || users[i].Region != "US" {
			t.Fatalf("upsert %d = %+v", i, users[i])
		}
	}
	snap, err := s.c.Collection("users").Doc(uid).Get(ctx)
	if err != nil {
		t.Fatalf("get doc: %v", err)
	}
	var d userDoc
	if err := snap.DataTo(&d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if d.Email != "a@example.com" || d.DisplayName != "Ada" || d.Region != "US" || d.CreatedAt.IsZero() {
		t.Fatalf("stored doc = %+v", d)
	}
	if d.CreatedAt.After(d.UpdatedAt) {
		t.Fatalf("createdAt %v after updatedAt %v", d.CreatedAt, d.UpdatedAt)
	}
}

// TestUpsertUserUnchangedDoesNotWrite: a sign-in that changes nothing must
// return the stored user without touching updatedAt.
func TestUpsertUserUnchangedDoesNotWrite(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := uniqueUID(t)
	defaults := json.RawMessage(`{"windows":{}}`)
	if _, err := s.UpsertUserByFirebaseUID(ctx, uid, "a@example.com", "Ada", defaults); err != nil {
		t.Fatalf("insert: %v", err)
	}
	updatedAt := func() time.Time {
		snap, err := s.c.Collection("users").Doc(uid).Get(ctx)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		var d userDoc
		if err := snap.DataTo(&d); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return d.UpdatedAt
	}
	before := updatedAt()
	time.Sleep(20 * time.Millisecond)
	// Same values, plus blank values that must not blank stored ones.
	for _, in := range [][2]string{{"a@example.com", "Ada"}, {"", "Ada"}, {"a@example.com", ""}, {"", ""}} {
		u, err := s.UpsertUserByFirebaseUID(ctx, uid, in[0], in[1], defaults)
		if err != nil {
			t.Fatalf("unchanged upsert %v: %v", in, err)
		}
		if u.Email != "a@example.com" || u.DisplayName != "Ada" {
			t.Fatalf("unchanged upsert %v = %+v", in, u)
		}
	}
	if after := updatedAt(); !after.Equal(before) {
		t.Fatalf("updatedAt changed on unchanged upsert: %v -> %v", before, after)
	}
}
