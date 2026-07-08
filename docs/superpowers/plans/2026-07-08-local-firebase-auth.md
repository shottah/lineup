# Local Firebase Auth: Emulator Harness + API Middleware (issue #8) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Firebase Auth Emulator harness plus the API's token-verification middleware and `/v1/me` endpoints, fully verifiable on the local stack with no cloud project.

**Architecture:** `fbauth` package isolates token verification behind a `TokenVerifier` interface (real impl = firebase-admin-go, honors `FIREBASE_AUTH_EMULATOR_HOST`; fake for tests). `RequireAuth` middleware verifies Bearer tokens and upserts the user row; `/v1/me` handlers depend on a narrow `UserStore` interface so unit tests are hermetic.

**Tech Stack:** Go 1.23, chi v5, pgx v5, firebase.google.com/go/v4, Firebase CLI emulator (Java 17 present), Postgres 16 (local: Docker on :5433).

## Global Constraints

- Branch `feat/8-auth-me` (off `main`), squash-merge. Commit after every task.
- Local project ID `demo-lineup` (demo- prefix = emulator guaranteed offline). Emulator port 9099. API env for local runs: `DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup?sslmode=disable' FIREBASE_PROJECT_ID=demo-lineup FIREBASE_AUTH_EMULATOR_HOST=localhost:9099 PORT=8080`.
- Prefs shape (issue-fixed, exact): `{"windows":{"mon":{"enabled":true,"start":"19:00","end":"23:00"},"tue":{…},"wed":{…},"thu":{…},"fri":{…},"sat":{…},"sun":{…}}}` — exactly the seven keys `mon tue wed thu fri sat sun`, `start`/`end` zero-padded 24h `HH:MM`, `start < end`. Defaults: all seven enabled 19:00–23:00.
- Error bodies: `{"error":"<code>"}` (+ optional `"detail"`). 401 unauthorized, 422 validation, 400 malformed JSON, 500 internal.
- Unit tests never require the emulator or Postgres. Store integration tests `t.Skip` unless `TEST_DATABASE_URL` is set. `go test ./...` must be green both with and without it.
- TDD per task: failing test → run (FAIL) → implement → run (PASS) → commit.
- `users` table columns (existing, do not migrate): `id BIGINT identity, firebase_uid TEXT NOT NULL UNIQUE, email TEXT NOT NULL, display_name TEXT NOT NULL DEFAULT '', region TEXT NOT NULL DEFAULT 'US', schedule_prefs JSONB NOT NULL DEFAULT '{}', created_at, updated_at`.

---

### Task 1: Emulator harness + local-dev doc

**Files:**
- Create: `firebase.json` (repo root)
- Create: `infra/local-dev.md`

**Interfaces:**
- Produces: running emulator recipe used by Task 7's live verification and by issue #15 later.

- [ ] **Step 1: Write `firebase.json`**

```json
{
  "emulators": {
    "auth": { "port": 9099 },
    "ui": { "enabled": false }
  }
}
```

- [ ] **Step 2: Write `infra/local-dev.md`**

```markdown
# Local development stack

Four processes, in start order. All local; no cloud access.

## 1. Postgres 16 (Docker, port 5433 — 5432 is often taken)

    docker run -d --name lineup-pg -p 127.0.0.1:5433:5432 \
      -e POSTGRES_USER=lineup -e POSTGRES_PASSWORD=lineup -e POSTGRES_DB=lineup postgres:16

Already created once? `docker start lineup-pg`.

## 2. Firebase Auth emulator (port 9099; needs Java ≥ 11)

    firebase emulators:start --only auth --project demo-lineup

`demo-` prefixed project IDs are guaranteed offline — the emulator cannot
touch real cloud resources with them. Do NOT use the real project ID here.

## 3. API (port 8080; migrations run at boot)

    cd api && DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup?sslmode=disable' \
      FIREBASE_PROJECT_ID=demo-lineup FIREBASE_AUTH_EMULATOR_HOST=localhost:9099 \
      PORT=8080 go run ./cmd/api

`FIREBASE_AUTH_EMULATOR_HOST` makes firebase-admin-go verify tokens against
the emulator. Omit it (and use the real project ID) to verify real tokens.

## 4. Web (port 3001 — 3000 is often taken)

    cd web && pnpm run dev --port 3001

## Minting a test ID token without the web app

    curl -s 'http://localhost:9099/identitytoolkit.googleapis.com/v1/accounts:signUp?key=any' \
      -H 'Content-Type: application/json' \
      -d '{"email":"dev@example.com","password":"password123","returnSecureToken":true}' | jq -r .idToken

Any non-empty `key=` works against the emulator. Use the token as
`Authorization: Bearer <idToken>` against `GET http://localhost:8080/v1/me`.

## Store integration tests

    cd api && TEST_DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup?sslmode=disable' go test ./internal/store/

Skipped automatically when `TEST_DATABASE_URL` is unset (CI stays hermetic).
```

- [ ] **Step 3: Verify the emulator boots**

Run: `firebase emulators:start --only auth --project demo-lineup` (background or second terminal), then `curl -s http://localhost:9099` → expect `{"authEmulator":{"ready":true}}`-style JSON. Stop it after.

- [ ] **Step 4: Commit**

```bash
git add firebase.json infra/local-dev.md
git commit -m "feat(dev): firebase auth emulator harness + local stack runbook"
```

### Task 2: `fbauth` package — Identity, TokenVerifier, Fake, real Verifier

**Files:**
- Create: `api/internal/fbauth/fbauth.go`
- Create: `api/internal/fbauth/fake.go`
- Create: `api/internal/fbauth/fake_test.go`
- Modify: `api/go.mod` (adds firebase.google.com/go/v4)

**Interfaces:**
- Produces: `fbauth.Identity{UID, Email, DisplayName string}`; `fbauth.TokenVerifier` interface `VerifyIDToken(ctx context.Context, rawToken string) (Identity, error)`; `fbauth.New(ctx, projectID) (*Verifier, error)`; `fbauth.Fake{Tokens map[string]Identity}`; `fbauth.ErrUnknownToken`. Tasks 5–6 consume all of these.

- [ ] **Step 1: Write the failing test**

```go
// api/internal/fbauth/fake_test.go
package fbauth

import (
	"context"
	"errors"
	"testing"
)

func TestFakeVerifyIDToken(t *testing.T) {
	f := &Fake{Tokens: map[string]Identity{
		"good-token": {UID: "u1", Email: "u1@example.com", DisplayName: "U One"},
	}}

	id, err := f.VerifyIDToken(context.Background(), "good-token")
	if err != nil {
		t.Fatalf("VerifyIDToken(good-token) error = %v", err)
	}
	if id.UID != "u1" || id.Email != "u1@example.com" || id.DisplayName != "U One" {
		t.Fatalf("VerifyIDToken(good-token) = %+v, want u1 identity", id)
	}

	if _, err := f.VerifyIDToken(context.Background(), "bad-token"); !errors.Is(err, ErrUnknownToken) {
		t.Fatalf("VerifyIDToken(bad-token) error = %v, want ErrUnknownToken", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/fbauth/`
Expected: FAIL (package does not compile: undefined Fake, Identity, ErrUnknownToken)

- [ ] **Step 3: Add the dependency and write the implementation**

Run: `cd api && go get firebase.google.com/go/v4@latest`

```go
// api/internal/fbauth/fbauth.go
//
// Package fbauth isolates Firebase ID-token verification behind the
// TokenVerifier interface so handlers and middleware can be tested without
// Firebase. The real Verifier honors FIREBASE_AUTH_EMULATOR_HOST, so the
// same code path verifies against the local Auth emulator in development
// and against live Firebase in production.
package fbauth

import (
	"context"
	"fmt"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
)

// Identity is what a verified ID token yields. Email is always present for
// the providers Lineup supports (users.email is NOT NULL); DisplayName may
// be empty.
type Identity struct {
	UID         string
	Email       string
	DisplayName string
}

// TokenVerifier verifies a raw Firebase ID token and returns the caller's
// identity claims.
type TokenVerifier interface {
	VerifyIDToken(ctx context.Context, rawToken string) (Identity, error)
}

// Verifier is the production TokenVerifier backed by firebase-admin-go.
type Verifier struct {
	client *auth.Client
}

// New builds a Verifier for projectID. Verification needs no service
// account credentials: signature checks use Google's public certs, and the
// emulator (FIREBASE_AUTH_EMULATOR_HOST) skips signatures entirely.
func New(ctx context.Context, projectID string) (*Verifier, error) {
	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: projectID})
	if err != nil {
		return nil, fmt.Errorf("fbauth: new app: %w", err)
	}
	client, err := app.Auth(ctx)
	if err != nil {
		return nil, fmt.Errorf("fbauth: auth client: %w", err)
	}
	return &Verifier{client: client}, nil
}

func (v *Verifier) VerifyIDToken(ctx context.Context, rawToken string) (Identity, error) {
	tok, err := v.client.VerifyIDToken(ctx, rawToken)
	if err != nil {
		return Identity{}, fmt.Errorf("fbauth: verify: %w", err)
	}
	id := Identity{UID: tok.UID}
	if e, ok := tok.Claims["email"].(string); ok {
		id.Email = e
	}
	if n, ok := tok.Claims["name"].(string); ok {
		id.DisplayName = n
	}
	return id, nil
}
```

```go
// api/internal/fbauth/fake.go
package fbauth

import (
	"context"
	"errors"
)

// ErrUnknownToken is returned by Fake for tokens it has no entry for.
var ErrUnknownToken = errors.New("fbauth: unknown token")

// Fake is a TokenVerifier for tests: it resolves raw token strings from a
// fixed map and never talks to Firebase.
type Fake struct {
	Tokens map[string]Identity
}

func (f *Fake) VerifyIDToken(_ context.Context, rawToken string) (Identity, error) {
	id, ok := f.Tokens[rawToken]
	if !ok {
		return Identity{}, ErrUnknownToken
	}
	return id, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api && go test ./internal/fbauth/ && go build ./...`
Expected: PASS; whole module still builds.

- [ ] **Step 5: Commit**

```bash
git add api/go.mod api/go.sum api/internal/fbauth/
git commit -m "feat(api): fbauth package with TokenVerifier, emulator-aware verifier, test fake"
```

### Task 3: `prefs` package — defaults and validation

**Files:**
- Create: `api/internal/prefs/prefs.go`
- Create: `api/internal/prefs/prefs_test.go`

**Interfaces:**
- Produces: `prefs.Default() json.RawMessage` (canonical all-days 19:00–23:00 JSON) and `prefs.Validate(raw json.RawMessage) error` (nil = valid). Tasks 5–6 consume both.

- [ ] **Step 1: Write the failing tests**

```go
// api/internal/prefs/prefs_test.go
package prefs

import (
	"encoding/json"
	"testing"
)

func TestDefaultIsValidAndCanonical(t *testing.T) {
	d := Default()
	if err := Validate(d); err != nil {
		t.Fatalf("Validate(Default()) = %v, want nil", err)
	}
	var p struct {
		Windows map[string]struct {
			Enabled bool   `json:"enabled"`
			Start   string `json:"start"`
			End     string `json:"end"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(d, &p); err != nil {
		t.Fatalf("Default() not JSON: %v", err)
	}
	if len(p.Windows) != 7 {
		t.Fatalf("Default() has %d windows, want 7", len(p.Windows))
	}
	mon := p.Windows["mon"]
	if !mon.Enabled || mon.Start != "19:00" || mon.End != "23:00" {
		t.Fatalf("Default() mon = %+v, want enabled 19:00-23:00", mon)
	}
}

func TestValidate(t *testing.T) {
	day := `{"enabled":true,"start":"19:00","end":"23:00"}`
	full := func(overrides map[string]string) string {
		out := `{"windows":{`
		for i, d := range []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"} {
			v := day
			if o, ok := overrides[d]; ok {
				v = o
			}
			if i > 0 {
				out += ","
			}
			out += `"` + d + `":` + v
		}
		return out + `}}`
	}

	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"valid default shape", full(nil), false},
		{"disabled day still needs valid times", full(map[string]string{"tue": `{"enabled":false,"start":"09:00","end":"10:30"}`}), false},
		{"missing day key", `{"windows":{"mon":` + day + `}}`, true},
		{"extra key", `{"windows":{"mon":` + day + `,"tue":` + day + `,"wed":` + day + `,"thu":` + day + `,"fri":` + day + `,"sat":` + day + `,"sun":` + day + `,"xxx":` + day + `}}`, true},
		{"bad time format", full(map[string]string{"fri": `{"enabled":true,"start":"7pm","end":"23:00"}`}), true},
		{"non-zero-padded hour", full(map[string]string{"fri": `{"enabled":true,"start":"9:00","end":"23:00"}`}), true},
		{"start not before end", full(map[string]string{"sat": `{"enabled":true,"start":"23:00","end":"19:00"}`}), true},
		{"start equals end", full(map[string]string{"sat": `{"enabled":true,"start":"19:00","end":"19:00"}`}), true},
		{"hour out of range", full(map[string]string{"sun": `{"enabled":true,"start":"24:00","end":"25:00"}`}), true},
		{"unknown field in window", full(map[string]string{"mon": `{"enabled":true,"start":"19:00","end":"23:00","x":1}`}), true},
		{"top-level unknown field", `{"windows":` + full(nil)[12:] + `,"x":1}`, true},
		{"not json", `{`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(json.RawMessage(tc.raw))
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd api && go test ./internal/prefs/`
Expected: FAIL (undefined Default, Validate)

- [ ] **Step 3: Write the implementation**

```go
// api/internal/prefs/prefs.go
//
// Package prefs owns the schedule_prefs JSON shape: the canonical default
// and validation for PATCH input. The shape is fixed by issue #8:
// {"windows":{"mon":{"enabled":true,"start":"19:00","end":"23:00"},...}}
// with exactly the seven lowercase day keys and zero-padded 24h HH:MM.
package prefs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
)

var dayKeys = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

var timeRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

type window struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start"`
	End     string `json:"end"`
}

type prefsDoc struct {
	Windows map[string]window `json:"windows"`
}

// Default returns the canonical default prefs: every day enabled
// 19:00-23:00. The result is freshly marshaled each call, so callers may
// not mutate shared state through it.
func Default() json.RawMessage {
	w := make(map[string]window, len(dayKeys))
	for _, d := range dayKeys {
		w[d] = window{Enabled: true, Start: "19:00", End: "23:00"}
	}
	raw, err := json.Marshal(prefsDoc{Windows: w})
	if err != nil {
		panic(fmt.Sprintf("prefs: marshal default: %v", err)) // static input; cannot fail
	}
	return raw
}

// Validate reports whether raw is a well-formed prefs document: exactly the
// seven day keys, HH:MM times, start strictly before end. nil means valid.
func Validate(raw json.RawMessage) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var doc prefsDoc
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("prefs: %w", err)
	}
	if dec.More() {
		return fmt.Errorf("prefs: trailing data after document")
	}
	if len(doc.Windows) != len(dayKeys) {
		return fmt.Errorf("prefs: windows must contain exactly the seven day keys")
	}
	for _, d := range dayKeys {
		w, ok := doc.Windows[d]
		if !ok {
			return fmt.Errorf("prefs: missing day %q", d)
		}
		if !timeRe.MatchString(w.Start) || !timeRe.MatchString(w.End) {
			return fmt.Errorf("prefs: %s: times must be zero-padded 24h HH:MM", d)
		}
		if w.Start >= w.End { // zero-padded HH:MM compares correctly as strings
			return fmt.Errorf("prefs: %s: start must be before end", d)
		}
	}
	return nil
}
```

Note: `DisallowUnknownFields` rejects unknown top-level and per-window
fields, but unknown *day keys* land in the map — that's why the length +
per-key presence checks together enforce "exactly these seven".

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api && go test ./internal/prefs/`
Expected: PASS (all subtests)

- [ ] **Step 5: Commit**

```bash
git add api/internal/prefs/
git commit -m "feat(api): schedule prefs defaults and validation"
```

### Task 4: store users methods (+ gated integration tests)

**Files:**
- Create: `api/internal/store/users.go`
- Create: `api/internal/store/users_test.go`

**Interfaces:**
- Consumes: existing `store.Store` (pgxpool), `users` table.
- Produces: `store.User{ID int64; FirebaseUID, Email, DisplayName, Region string; SchedulePrefs json.RawMessage}` with JSON tags `id/email/display_name/region/schedule_prefs` (FirebaseUID tagged `-`); `(*Store).UpsertUserByFirebaseUID(ctx, firebaseUID, email, displayName string, defaultPrefs json.RawMessage) (*User, error)`; `(*Store).UpdateUserPrefs(ctx, userID int64, region *string, prefsJSON json.RawMessage) (*User, error)`. Tasks 5–6 consume these exact signatures.

- [ ] **Step 1: Write the failing integration test**

```go
// api/internal/store/users_test.go
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
	u2, err := s.UpsertUserByFirebaseUID(ctx, uid, "b@example.com", "", defaults)
	if err != nil {
		t.Fatalf("update upsert: %v", err)
	}
	if u2.ID != u1.ID {
		t.Fatalf("update upsert created new row: id %d != %d", u2.ID, u1.ID)
	}
	if u2.Email != "b@example.com" || u2.DisplayName != "Ada" {
		t.Fatalf("update upsert = %+v, want email updated, display name kept", u2)
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
}
```

- [ ] **Step 2: Run to verify failure (compile error), and that skip works**

Run: `cd api && TEST_DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup?sslmode=disable' go test ./internal/store/`
Expected: FAIL (undefined UpsertUserByFirebaseUID etc.)
Also run without the env var: `go test ./internal/store/` → existing tests pass, new ones SKIP (once compiling).

- [ ] **Step 3: Write the implementation**

```go
// api/internal/store/users.go
package store

import (
	"context"
	"encoding/json"
	"fmt"
)

// User mirrors a row of the users table. FirebaseUID is intentionally
// excluded from JSON responses.
type User struct {
	ID            int64           `json:"id"`
	FirebaseUID   string          `json:"-"`
	Email         string          `json:"email"`
	DisplayName   string          `json:"display_name"`
	Region        string          `json:"region"`
	SchedulePrefs json.RawMessage `json:"schedule_prefs"`
}

const userReturning = `id, firebase_uid, email, display_name, region, schedule_prefs`

// UpsertUserByFirebaseUID inserts the user on first sight (applying
// defaultPrefs) or refreshes email/display_name on subsequent sign-ins.
// An empty displayName never overwrites a previously stored one.
func (s *Store) UpsertUserByFirebaseUID(ctx context.Context, firebaseUID, email, displayName string, defaultPrefs json.RawMessage) (*User, error) {
	const q = `
INSERT INTO users (firebase_uid, email, display_name, schedule_prefs)
VALUES ($1, $2, $3, $4)
ON CONFLICT (firebase_uid) DO UPDATE SET
  email = EXCLUDED.email,
  display_name = CASE WHEN EXCLUDED.display_name <> '' THEN EXCLUDED.display_name ELSE users.display_name END,
  updated_at = now()
RETURNING ` + userReturning
	u := &User{}
	err := s.Pool.QueryRow(ctx, q, firebaseUID, email, displayName, defaultPrefs).
		Scan(&u.ID, &u.FirebaseUID, &u.Email, &u.DisplayName, &u.Region, &u.SchedulePrefs)
	if err != nil {
		return nil, fmt.Errorf("store: upsert user: %w", err)
	}
	return u, nil
}

// UpdateUserPrefs updates region and/or schedule_prefs; nil arguments leave
// the corresponding column untouched.
func (s *Store) UpdateUserPrefs(ctx context.Context, userID int64, region *string, prefsJSON json.RawMessage) (*User, error) {
	const q = `
UPDATE users SET
  region = COALESCE($2, region),
  schedule_prefs = COALESCE($3, schedule_prefs),
  updated_at = now()
WHERE id = $1
RETURNING ` + userReturning
	u := &User{}
	err := s.Pool.QueryRow(ctx, q, userID, region, prefsJSON).
		Scan(&u.ID, &u.FirebaseUID, &u.Email, &u.DisplayName, &u.Region, &u.SchedulePrefs)
	if err != nil {
		return nil, fmt.Errorf("store: update user prefs: %w", err)
	}
	return u, nil
}
```

- [ ] **Step 4: Run tests to verify they pass (both modes)**

Run: `cd api && TEST_DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup?sslmode=disable' go test ./internal/store/` → PASS
Run: `cd api && go test ./internal/store/` → PASS (new tests skip)
(Postgres from `infra/local-dev.md` §1 must be running for the first command.)

- [ ] **Step 5: Commit**

```bash
git add api/internal/store/users.go api/internal/store/users_test.go
git commit -m "feat(api): user upsert and prefs update store methods"
```

### Task 5: RequireAuth middleware + user context

**Files:**
- Create: `api/internal/httpserver/auth.go`
- Create: `api/internal/httpserver/auth_test.go`

**Interfaces:**
- Consumes: `fbauth.TokenVerifier`, `fbauth.Fake`, `fbauth.ErrUnknownToken`, `prefs.Default()`, `store.User`.
- Produces: `UserStore` interface (methods exactly matching Task 4's two signatures); `requireAuth(v fbauth.TokenVerifier, users UserStore) func(http.Handler) http.Handler`; `userFrom(ctx) *store.User`; `writeJSONError(w, status int, code string)`. Task 6 consumes all of these.

- [ ] **Step 1: Write the failing tests**

```go
// api/internal/httpserver/auth_test.go
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shottah/lineup/api/internal/fbauth"
	"github.com/shottah/lineup/api/internal/store"
)

// fakeUsers implements UserStore in memory.
type fakeUsers struct {
	byUID     map[string]*store.User
	nextID    int64
	upsertErr error
	updateErr error
}

func newFakeUsers() *fakeUsers {
	return &fakeUsers{byUID: map[string]*store.User{}, nextID: 1}
}

func (f *fakeUsers) UpsertUserByFirebaseUID(_ context.Context, uid, email, displayName string, defaultPrefs json.RawMessage) (*store.User, error) {
	if f.upsertErr != nil {
		return nil, f.upsertErr
	}
	if u, ok := f.byUID[uid]; ok {
		u.Email = email
		if displayName != "" {
			u.DisplayName = displayName
		}
		cp := *u
		return &cp, nil
	}
	u := &store.User{ID: f.nextID, FirebaseUID: uid, Email: email, DisplayName: displayName, Region: "US", SchedulePrefs: defaultPrefs}
	f.nextID++
	f.byUID[uid] = u
	cp := *u
	return &cp, nil
}

func (f *fakeUsers) UpdateUserPrefs(_ context.Context, userID int64, region *string, prefsJSON json.RawMessage) (*store.User, error) {
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	for _, u := range f.byUID {
		if u.ID == userID {
			if region != nil {
				u.Region = *region
			}
			if prefsJSON != nil {
				u.SchedulePrefs = prefsJSON
			}
			cp := *u
			return &cp, nil
		}
	}
	return nil, errors.New("not found")
}

func authedHandler(t *testing.T) (http.Handler, *fakeUsers) {
	t.Helper()
	users := newFakeUsers()
	verifier := &fbauth.Fake{Tokens: map[string]fbauth.Identity{
		"tok-1": {UID: "uid-1", Email: "one@example.com", DisplayName: "One"},
	}}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := userFrom(r.Context())
		if u == nil {
			t.Fatal("userFrom returned nil inside authed handler")
		}
		w.WriteHeader(http.StatusOK)
	})
	return requireAuth(verifier, users)(inner), users
}

func TestRequireAuth(t *testing.T) {
	h, users := authedHandler(t)

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"missing header", "", http.StatusUnauthorized},
		{"not bearer", "Basic abc", http.StatusUnauthorized},
		{"unknown token", "Bearer nope", http.StatusUnauthorized},
		{"valid token", "Bearer tok-1", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusUnauthorized {
				var body map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] != "unauthorized" {
					t.Fatalf("401 body = %s, want {\"error\":\"unauthorized\"}", rec.Body.String())
				}
			}
		})
	}

	if users.byUID["uid-1"] == nil {
		t.Fatal("valid request did not upsert the user")
	}
}

func TestRequireAuthUpsertFailure(t *testing.T) {
	users := newFakeUsers()
	users.upsertErr = errors.New("db down")
	verifier := &fbauth.Fake{Tokens: map[string]fbauth.Identity{"tok-1": {UID: "u", Email: "e@x.com"}}}
	h := requireAuth(verifier, users)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("inner handler must not run when upsert fails")
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer tok-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd api && go test ./internal/httpserver/`
Expected: FAIL (undefined requireAuth, userFrom, UserStore)

- [ ] **Step 3: Write the implementation**

```go
// api/internal/httpserver/auth.go
package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/shottah/lineup/api/internal/fbauth"
	"github.com/shottah/lineup/api/internal/prefs"
	"github.com/shottah/lineup/api/internal/store"
)

// UserStore is the slice of *store.Store the auth layer needs; narrowed to
// an interface so handler tests run without Postgres.
type UserStore interface {
	UpsertUserByFirebaseUID(ctx context.Context, firebaseUID, email, displayName string, defaultPrefs json.RawMessage) (*store.User, error)
	UpdateUserPrefs(ctx context.Context, userID int64, region *string, prefsJSON json.RawMessage) (*store.User, error)
}

type ctxKey int

const userKey ctxKey = 0

// userFrom returns the authenticated user placed on the context by
// requireAuth, or nil outside an authenticated request.
func userFrom(ctx context.Context) *store.User {
	u, _ := ctx.Value(userKey).(*store.User)
	return u
}

func writeJSONError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":%q}`, code)
}

// requireAuth verifies the Bearer token, upserts the user row (first sign-in
// gets default schedule prefs), and stashes the user on the context.
func requireAuth(v fbauth.TokenVerifier, users UserStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			const bearer = "Bearer "
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, bearer) {
				writeJSONError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			ident, err := v.VerifyIDToken(r.Context(), strings.TrimPrefix(h, bearer))
			if err != nil {
				writeJSONError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			u, err := users.UpsertUserByFirebaseUID(r.Context(), ident.UID, ident.Email, ident.DisplayName, prefs.Default())
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
		})
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api && go test ./internal/httpserver/`
Expected: PASS (including the pre-existing healthz test)

- [ ] **Step 5: Commit**

```bash
git add api/internal/httpserver/auth.go api/internal/httpserver/auth_test.go
git commit -m "feat(api): RequireAuth middleware with user upsert"
```

### Task 6: /v1/me handlers, routing, config + main wiring

**Files:**
- Create: `api/internal/httpserver/me.go`
- Create: `api/internal/httpserver/me_test.go`
- Modify: `api/internal/httpserver/server.go` (Deps + routes)
- Modify: `api/internal/config/config.go` (+ its test) — `FIREBASE_PROJECT_ID` now required
- Modify: `api/cmd/api/main.go` (verifier construction + Deps)

**Interfaces:**
- Consumes: Task 5's `requireAuth`/`userFrom`/`writeJSONError`/`UserStore`, Task 3's `prefs.Validate`, Task 2's `fbauth.New`.
- Produces: `GET /v1/me`, `PATCH /v1/me`; `Deps{Store *store.Store; Users UserStore; Verifier fbauth.TokenVerifier}`; `config.ErrMissingFirebaseProjectID`.

- [ ] **Step 1: Write the failing tests**

```go
// api/internal/httpserver/me_test.go
package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shottah/lineup/api/internal/fbauth"
)

func testServer(t *testing.T) (http.Handler, *fakeUsers) {
	t.Helper()
	users := newFakeUsers()
	verifier := &fbauth.Fake{Tokens: map[string]fbauth.Identity{
		"tok-1": {UID: "uid-1", Email: "one@example.com", DisplayName: "One"},
	}}
	srv := New(Deps{Users: users, Verifier: verifier})
	return srv.Handler, users
}

func do(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const validPrefs = `{"windows":{"mon":{"enabled":true,"start":"18:00","end":"22:00"},"tue":{"enabled":true,"start":"19:00","end":"23:00"},"wed":{"enabled":true,"start":"19:00","end":"23:00"},"thu":{"enabled":true,"start":"19:00","end":"23:00"},"fri":{"enabled":true,"start":"19:00","end":"23:00"},"sat":{"enabled":true,"start":"19:00","end":"23:00"},"sun":{"enabled":true,"start":"19:00","end":"23:00"}}}`

func TestGetMe(t *testing.T) {
	h, _ := testServer(t)

	if rec := do(t, h, http.MethodGet, "/v1/me", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET /v1/me = %d, want 401", rec.Code)
	}

	rec := do(t, h, http.MethodGet, "/v1/me", "tok-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/me = %d, body %s", rec.Code, rec.Body.String())
	}
	var u struct {
		ID            int64           `json:"id"`
		Email         string          `json:"email"`
		Region        string          `json:"region"`
		SchedulePrefs json.RawMessage `json:"schedule_prefs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil {
		t.Fatalf("GET /v1/me body not JSON: %v", err)
	}
	if u.Email != "one@example.com" || u.Region != "US" {
		t.Fatalf("GET /v1/me = %+v", u)
	}
	if !strings.Contains(string(u.SchedulePrefs), `"19:00"`) {
		t.Fatalf("first sign-in did not get default prefs: %s", u.SchedulePrefs)
	}
	if strings.Contains(rec.Body.String(), "uid-1") {
		t.Fatalf("response leaks firebase uid: %s", rec.Body.String())
	}
}

func TestPatchMe(t *testing.T) {
	h, _ := testServer(t)

	cases := []struct {
		name string
		body string
		want int
	}{
		{"update region", `{"region":"GB"}`, http.StatusOK},
		{"update prefs", `{"schedule_prefs":` + validPrefs + `}`, http.StatusOK},
		{"update both", `{"region":"CA","schedule_prefs":` + validPrefs + `}`, http.StatusOK},
		{"invalid prefs shape", `{"schedule_prefs":{"windows":{"mon":{"enabled":true,"start":"25:00","end":"26:00"}}}}`, http.StatusUnprocessableEntity},
		{"empty region", `{"region":""}`, http.StatusUnprocessableEntity},
		{"empty body object", `{}`, http.StatusUnprocessableEntity},
		{"malformed json", `{`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPatch, "/v1/me", "tok-1", tc.body)
			if rec.Code != tc.want {
				t.Fatalf("PATCH /v1/me %s = %d, want %d (body %s)", tc.body, rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	rec := do(t, h, http.MethodGet, "/v1/me", "tok-1", "")
	if !strings.Contains(rec.Body.String(), `"CA"`) || !strings.Contains(rec.Body.String(), `"18:00"`) {
		t.Fatalf("PATCH results not persisted: %s", rec.Body.String())
	}
}
```

Also update `api/internal/config/config_test.go`: every existing case that
sets only `DATABASE_URL` must now also set `FIREBASE_PROJECT_ID`, plus one
new case asserting `Load()` returns `ErrMissingFirebaseProjectID` when it is
unset (mirror the existing missing-DATABASE_URL test's structure exactly).

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd api && go test ./internal/httpserver/ ./internal/config/`
Expected: FAIL (Deps has no Users/Verifier fields; ErrMissingFirebaseProjectID undefined)

- [ ] **Step 3: Write the implementation**

`api/internal/httpserver/me.go`:

```go
// api/internal/httpserver/me.go
package httpserver

import (
	"encoding/json"
	"net/http"

	"github.com/shottah/lineup/api/internal/prefs"
)

func handleGetMe(w http.ResponseWriter, r *http.Request) {
	writeUser(w, r)
}

func handlePatchMe(users UserStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Region        *string         `json:"region"`
			SchedulePrefs json.RawMessage `json:"schedule_prefs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "malformed json")
			return
		}
		if body.Region == nil && body.SchedulePrefs == nil {
			writeJSONError(w, http.StatusUnprocessableEntity, "nothing to update")
			return
		}
		if body.Region != nil && *body.Region == "" {
			writeJSONError(w, http.StatusUnprocessableEntity, "region must be non-empty")
			return
		}
		if body.SchedulePrefs != nil {
			if err := prefs.Validate(body.SchedulePrefs); err != nil {
				writeJSONError(w, http.StatusUnprocessableEntity, "invalid schedule_prefs")
				return
			}
		}
		u := userFrom(r.Context())
		updated, err := users.UpdateUserPrefs(r.Context(), u.ID, body.Region, body.SchedulePrefs)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(updated)
	}
}

func writeUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(userFrom(r.Context()))
}
```

`api/internal/httpserver/server.go` becomes:

```go
package httpserver

import (
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/shottah/lineup/api/internal/fbauth"
	"github.com/shottah/lineup/api/internal/store"
)

type Deps struct {
	Store    *store.Store
	Users    UserStore
	Verifier fbauth.TokenVerifier
}

func New(d Deps) *http.Server {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Logger, middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})
	if d.Verifier != nil && d.Users != nil {
		r.Route("/v1", func(v1 chi.Router) {
			v1.Use(requireAuth(d.Verifier, d.Users))
			v1.Get("/me", handleGetMe)
			v1.Patch("/me", handlePatchMe(d.Users))
		})
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	return &http.Server{Addr: ":" + port, Handler: r}
}
```

`api/internal/config/config.go`: add
`var ErrMissingFirebaseProjectID = errors.New("config: FIREBASE_PROJECT_ID is required")`,
return it from `Load` when `cfg.FirebaseProjectID == ""` (after the
DATABASE_URL check), and update the doc comment (FIREBASE_PROJECT_ID is now
consumed by this task, not "task 8 later").

`api/cmd/api/main.go`: after the existing `store.New` call, add:

```go
	verifier, err := fbauth.New(ctx, cfg.FirebaseProjectID)
	if err != nil {
		log.Fatalf("fbauth: %v", err)
	}
```

and change the Deps construction to
`httpserver.New(httpserver.Deps{Store: st, Users: st, Verifier: verifier})`
(add the `fbauth` import; match the file's existing log.Fatal error style).

- [ ] **Step 4: Run the full suite**

Run: `cd api && go vet ./... && go test ./...`
Expected: PASS everywhere; store tests skip without TEST_DATABASE_URL.

- [ ] **Step 5: Commit**

```bash
git add api/
git commit -m "feat(api): /v1/me endpoints behind firebase auth middleware"
```

### Task 7: Live emulator verification + PR

**Files:** none (verification + PR assembly)

**Interfaces:**
- Consumes: everything above; `infra/local-dev.md` recipe.

- [ ] **Step 1: Full local boot** — Postgres (`docker start lineup-pg` or the run command), emulator (`firebase emulators:start --only auth --project demo-lineup`), API with the env from Global Constraints.
- [ ] **Step 2: Mint a token** via the local-dev.md curl (signUp endpoint), capture `idToken`.
- [ ] **Step 3: Exercise the real path**

```bash
curl -si -H "Authorization: Bearer $IDTOKEN" http://localhost:8080/v1/me                       # want 200, default prefs
curl -si -X PATCH -H "Authorization: Bearer $IDTOKEN" -H 'Content-Type: application/json' \
  -d '{"region":"GB"}' http://localhost:8080/v1/me                                             # want 200, region GB
curl -si http://localhost:8080/v1/me                                                           # want 401
docker exec lineup-pg psql -U lineup -d lineup -c "SELECT id, email, region FROM users;"       # want the emulator user row
```

- [ ] **Step 4: Verify acceptance from the issue** — tests cover 401/200+upsert/prefs validation (Tasks 5–6), `go test ./...` green (Task 6 Step 4), plus the live e2e above.
- [ ] **Step 5: Push and open PR** titled `feat(api): firebase auth middleware and me endpoints`, body: closes #8; notes the emulator harness + local-dev.md addition and that `FIREBASE_PROJECT_ID` is now required at boot (prod already sets it). Squash-merge per issue workflow (user's call).

---

## Self-review notes

- Spec coverage: harness (T1), TokenVerifier+fake (T2), prefs shape/defaults/422 source (T3), store methods (T4), middleware 401/upsert (T5), endpoints + config requirement + wiring (T6), live acceptance + PR (T7).
- Type consistency: `UserStore` (T5) signatures match T4's methods verbatim; `fbauth.Fake` map shape matches T2; `Deps{Store, Users, Verifier}` consistent between T6's server.go and main.go.
- The `store.User` JSON tags make GET/PATCH responses correct by construction and exclude `firebase_uid` (asserted in T6's leak test).
