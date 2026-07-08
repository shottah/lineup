# TVMaze Client (issue #10) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `internal/tvmaze` client — IMDB lookup and episode listing with timeout, rate limiting, and single-retry, tested hermetically against fixtures.

**Architecture:** One `Client` with a private `get(ctx, path, out)` helper owning the policy layer (limiter wait → request → status handling → single retry → JSON decode). Public methods are thin wrappers. Tests inject an httptest server via `NewWithBaseURL`.

**Tech Stack:** Go 1.25, stdlib net/http + encoding/json, golang.org/x/time/rate.

## Global Constraints

- Branch `feat/10-tvmaze-client` (off `main`), squash-merge. Commit per task.
- Package path `api/internal/tvmaze`; module `github.com/shottah/lineup/api`.
- Error style: wrapped, `tvmaze:`-prefixed (matches store/fbauth). Sentinel: `var ErrNotFound = errors.New("tvmaze: not found")`.
- `Show{ID, Name, Status}` / `Episode{Season, Number, AirDate string}` — exact field sets; `AirDate` empty string for unscheduled (never a parse error).
- Policy numbers (spec-fixed): timeout 10s; limiter `rate.NewLimiter(rate.Every(600*time.Millisecond), 1)`; exactly ONE retry, only on 429 or ≥500, honoring `Retry-After` seconds else 500ms; 404 → `ErrNotFound`, no retry.
- Tests: hermetic (httptest only, no network), fixtures under `api/internal/tvmaze/testdata/` with realistic full TVMaze shapes. In tests, always construct the client with a fast limiter is NOT allowed — use the real constructor; the 600ms limiter only delays the *second* request in a test (burst 1 covers the first), so keep per-test request counts ≤3 and total added wall time stays ~1-2s.
- TDD per task: failing test → FAIL run → implement → PASS run → commit.

---

### Task 1: Client scaffold + LookupByIMDB

**Files:**
- Create: `api/internal/tvmaze/tvmaze.go`
- Create: `api/internal/tvmaze/tvmaze_test.go`
- Create: `api/internal/tvmaze/testdata/lookup_show.json`
- Modify: `api/go.mod` (+ golang.org/x/time)

**Interfaces:**
- Produces: `New()`, `NewWithBaseURL(base string)`, `ErrNotFound`, `Show`, `(*Client).LookupByIMDB(ctx, imdbID) (Show, error)`, and the private `get` helper Task 2 reuses. Task 3 extends `get` with retry (its base behavior here: no retry yet).

- [ ] **Step 1: Write the fixture** `testdata/lookup_show.json` (real TVMaze `/lookup/shows?imdb=` response shape — extra fields deliberate, decoding must ignore them):

```json
{
  "id": 82,
  "url": "https://www.tvmaze.com/shows/82/game-of-thrones",
  "name": "Game of Thrones",
  "type": "Scripted",
  "language": "English",
  "genres": ["Drama", "Adventure", "Fantasy"],
  "status": "Ended",
  "runtime": 60,
  "averageRuntime": 61,
  "premiered": "2011-04-17",
  "ended": "2019-05-19",
  "officialSite": "http://www.hbo.com/game-of-thrones",
  "schedule": { "time": "21:00", "days": ["Sunday"] },
  "rating": { "average": 8.9 },
  "weight": 98,
  "network": { "id": 8, "name": "HBO", "country": { "name": "United States", "code": "US", "timezone": "America/New_York" } },
  "externals": { "tvrage": 24493, "thetvdb": 121361, "imdb": "tt0944947" },
  "updated": 1718009623
}
```

- [ ] **Step 2: Write the failing tests**

```go
// api/internal/tvmaze/tvmaze_test.go
package tvmaze

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// fixture reads a testdata file or fails the test.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return b
}

func TestLookupByIMDB(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "lookup_show.json"))
	}))
	defer srv.Close()

	c := NewWithBaseURL(srv.URL)
	show, err := c.LookupByIMDB(context.Background(), "tt0944947")
	if err != nil {
		t.Fatalf("LookupByIMDB: %v", err)
	}
	if gotPath != "/lookup/shows?imdb=tt0944947" {
		t.Fatalf("requested %q, want /lookup/shows?imdb=tt0944947", gotPath)
	}
	if show.ID != 82 || show.Name != "Game of Thrones" || show.Status != "Ended" {
		t.Fatalf("show = %+v, want id 82 / Game of Thrones / Ended", show)
	}
}

func TestLookupByIMDBNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"name":"Not Found","message":"","code":0,"status":404}`, http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := NewWithBaseURL(srv.URL).LookupByIMDB(context.Background(), "tt0000000")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 3: Run to verify failure**

Run: `cd api && go test ./internal/tvmaze/`
Expected: FAIL (package missing / undefined identifiers)

- [ ] **Step 4: Implement**

Run: `cd api && go get golang.org/x/time@latest`

```go
// api/internal/tvmaze/tvmaze.go
//
// Package tvmaze is a minimal client for the TVMaze API (keyless), used to
// resolve series by IMDB id and fetch real episode air dates for
// currently-airing shows. Requests are rate-limited under TVMaze's
// documented 20-calls-per-10s cap and retried once on 429/5xx.
package tvmaze

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/time/rate"
)

const defaultBaseURL = "https://api.tvmaze.com"

// ErrNotFound is returned when TVMaze has no entry for the requested
// resource. For IMDB lookups this is a normal outcome (movies and many
// older shows have no TVMaze entry), not a failure to log.
var ErrNotFound = errors.New("tvmaze: not found")

// Show is the subset of a TVMaze show record Lineup consumes: ID feeds
// titles.tvmaze_id, Status feeds airing status.
type Show struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// Episode is the subset of a TVMaze episode record Lineup consumes.
// AirDate is "YYYY-MM-DD", or "" when the episode has no scheduled date
// yet; parsing dates is the ingestion layer's concern.
type Episode struct {
	Season  int    `json:"season"`
	Number  int    `json:"number"`
	AirDate string `json:"airdate"`
}

// Client talks to TVMaze with a bounded timeout, a token-bucket rate limit
// under TVMaze's cap, and a single retry on 429/5xx.
type Client struct {
	baseURL string
	http    *http.Client
	limiter *rate.Limiter
}

// New returns a production Client against api.tvmaze.com.
func New() *Client {
	return NewWithBaseURL(defaultBaseURL)
}

// NewWithBaseURL returns a Client against base — the test seam for
// httptest servers.
func NewWithBaseURL(base string) *Client {
	return &Client{
		baseURL: base,
		http:    &http.Client{Timeout: 10 * time.Second},
		limiter: rate.NewLimiter(rate.Every(600*time.Millisecond), 1),
	}
}

// LookupByIMDB resolves an IMDB id (e.g. "tt0944947") to a TVMaze show.
func (c *Client) LookupByIMDB(ctx context.Context, imdbID string) (Show, error) {
	var s Show
	path := "/lookup/shows?imdb=" + url.QueryEscape(imdbID)
	if err := c.get(ctx, path, &s); err != nil {
		return Show{}, err
	}
	return s, nil
}

// get performs one rate-limited GET, decoding a 200 JSON body into out.
// 404 maps to ErrNotFound. (Retry on 429/5xx lands in a later task.)
func (c *Client) get(ctx context.Context, path string, out any) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("tvmaze: rate limit wait: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("tvmaze: new request: %w", err)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("tvmaze: %s: %w", path, err)
	}
	defer res.Body.Close()

	switch {
	case res.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case res.StatusCode != http.StatusOK:
		return fmt.Errorf("tvmaze: %s: unexpected status %d", path, res.StatusCode)
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("tvmaze: %s: decode: %w", path, err)
	}
	return nil
}

// strconv is used by Episodes (Task 2); referenced here so the import
// block stays stable across tasks.
var _ = strconv.Itoa
```

- [ ] **Step 5: Run to verify pass**

Run: `cd api && go test ./internal/tvmaze/ && go vet ./...`
Expected: PASS, vet clean.

- [ ] **Step 6: Commit**

```bash
git add api/go.mod api/go.sum api/internal/tvmaze/
git commit -m "feat(api): tvmaze client with rate-limited IMDB lookup"
```

### Task 2: Episodes + blank-air-date tolerance

**Files:**
- Create: `api/internal/tvmaze/testdata/episodes.json`
- Modify: `api/internal/tvmaze/tvmaze.go` (add `Episodes`; delete the `var _ = strconv.Itoa` placeholder line)
- Modify: `api/internal/tvmaze/tvmaze_test.go` (add test)

**Interfaces:**
- Consumes: Task 1's `get`, `Episode`.
- Produces: `(*Client).Episodes(ctx, showID int) ([]Episode, error)`.

- [ ] **Step 1: Write the fixture** `testdata/episodes.json` (array shape; extra fields deliberate; one episode with `"airdate": ""` and one with `"airdate": null`):

```json
[
  {
    "id": 4952,
    "url": "https://www.tvmaze.com/episodes/4952/example",
    "name": "Winter Is Coming",
    "season": 1,
    "number": 1,
    "type": "regular",
    "airdate": "2011-04-17",
    "airtime": "21:00",
    "airstamp": "2011-04-18T01:00:00+00:00",
    "runtime": 60,
    "rating": { "average": 8.0 },
    "summary": "<p>Example summary.</p>"
  },
  {
    "id": 4953,
    "name": "The Kingsroad",
    "season": 1,
    "number": 2,
    "type": "regular",
    "airdate": "",
    "airtime": "",
    "runtime": 60
  },
  {
    "id": 4954,
    "name": "Unscheduled Special",
    "season": 2,
    "number": 1,
    "type": "regular",
    "airdate": null,
    "runtime": null
  }
]
```

- [ ] **Step 2: Write the failing test** (append to tvmaze_test.go):

```go
func TestEpisodes(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "episodes.json"))
	}))
	defer srv.Close()

	eps, err := NewWithBaseURL(srv.URL).Episodes(context.Background(), 82)
	if err != nil {
		t.Fatalf("Episodes: %v", err)
	}
	if gotPath != "/shows/82/episodes" {
		t.Fatalf("requested %q, want /shows/82/episodes", gotPath)
	}
	if len(eps) != 3 {
		t.Fatalf("len = %d, want 3", len(eps))
	}
	if eps[0].Season != 1 || eps[0].Number != 1 || eps[0].AirDate != "2011-04-17" {
		t.Fatalf("eps[0] = %+v", eps[0])
	}
	// Blank and null air dates both decode to "" without error.
	if eps[1].AirDate != "" || eps[2].AirDate != "" {
		t.Fatalf("blank/null airdates = %q, %q; want empty strings", eps[1].AirDate, eps[2].AirDate)
	}
	if eps[2].Season != 2 || eps[2].Number != 1 {
		t.Fatalf("eps[2] = %+v", eps[2])
	}
}
```

- [ ] **Step 3: Run to verify failure** — `cd api && go test ./internal/tvmaze/` → FAIL (undefined Episodes)

- [ ] **Step 4: Implement** — add to tvmaze.go (and remove the `var _ = strconv.Itoa` line):

```go
// Episodes lists every episode TVMaze knows for a show, including
// not-yet-aired ones (blank AirDate).
func (c *Client) Episodes(ctx context.Context, showID int) ([]Episode, error) {
	var eps []Episode
	path := "/shows/" + strconv.Itoa(showID) + "/episodes"
	if err := c.get(ctx, path, &eps); err != nil {
		return nil, err
	}
	return eps, nil
}
```

- [ ] **Step 5: Run to verify pass** — `cd api && go test ./internal/tvmaze/ && go vet ./...` → PASS
- [ ] **Step 6: Commit**

```bash
git add api/internal/tvmaze/
git commit -m "feat(api): tvmaze episode listing tolerating blank air dates"
```

### Task 3: retry on 429/5xx

**Files:**
- Modify: `api/internal/tvmaze/tvmaze.go` (retry inside `get`)
- Modify: `api/internal/tvmaze/tvmaze_test.go` (retry tests)

**Interfaces:**
- Consumes/extends: Task 1's `get`. Public surface unchanged.

- [ ] **Step 1: Write the failing tests** (append):

```go
func TestRetryOn429ThenSuccess(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "lookup_show.json"))
	}))
	defer srv.Close()

	show, err := NewWithBaseURL(srv.URL).LookupByIMDB(context.Background(), "tt0944947")
	if err != nil {
		t.Fatalf("LookupByIMDB after 429: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (exactly one retry)", calls)
	}
	if show.ID != 82 {
		t.Fatalf("show = %+v", show)
	}
}

func TestNoSecondRetryOn500(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := NewWithBaseURL(srv.URL).LookupByIMDB(context.Background(), "tt0944947")
	if err == nil {
		t.Fatal("want error after repeated 500s")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatal("500 must not map to ErrNotFound")
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want exactly 2 (one retry, then give up)", calls)
	}
}

func TestNotFoundDoesNotRetry(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := NewWithBaseURL(srv.URL).LookupByIMDB(context.Background(), "tt0000000")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (404 never retries)", calls)
	}
}
```

- [ ] **Step 2: Run to verify failure** — the 429 test fails (no retry yet: unexpected status error, calls == 1).

- [ ] **Step 3: Implement** — restructure `get`'s body into a bounded loop:

```go
// get performs a rate-limited GET with a single retry on 429/5xx
// (honoring Retry-After seconds, else 500ms), decoding a 200 JSON body
// into out. 404 maps to ErrNotFound and never retries.
func (c *Client) get(ctx context.Context, path string, out any) error {
	const maxAttempts = 2
	for attempt := 1; ; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return fmt.Errorf("tvmaze: rate limit wait: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
		if err != nil {
			return fmt.Errorf("tvmaze: new request: %w", err)
		}
		res, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("tvmaze: %s: %w", path, err)
		}

		retryable := res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500
		if retryable && attempt < maxAttempts {
			delay := 500 * time.Millisecond
			if s := res.Header.Get("Retry-After"); s != "" {
				if secs, perr := strconv.Atoi(s); perr == nil && secs >= 0 {
					delay = time.Duration(secs) * time.Second
				}
			}
			res.Body.Close()
			select {
			case <-ctx.Done():
				return fmt.Errorf("tvmaze: %s: %w", path, ctx.Err())
			case <-time.After(delay):
			}
			continue
		}

		defer res.Body.Close()
		switch {
		case res.StatusCode == http.StatusNotFound:
			return ErrNotFound
		case res.StatusCode != http.StatusOK:
			return fmt.Errorf("tvmaze: %s: unexpected status %d", path, res.StatusCode)
		}
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			return fmt.Errorf("tvmaze: %s: decode: %w", path, err)
		}
		return nil
	}
}
```

- [ ] **Step 4: Run full verification** — `cd api && go test ./internal/tvmaze/ -v && go vet ./... && go test ./...` → all PASS (whole-suite run guards the rest of the module).
- [ ] **Step 5: Commit**

```bash
git add api/internal/tvmaze/
git commit -m "feat(api): single retry on 429/5xx honoring Retry-After"
```

### Task 4: PR (controller-inline)

- [ ] **Step 1:** `cd api && go vet ./... && go test ./...` one last time from a clean state.
- [ ] **Step 2:** Push branch; open PR titled `feat(api): tvmaze client` — body: closes #10; deliverables + policy summary (timeout/limiter/retry per design spec's external-API policy, self-contained pending a second client); note tests are hermetic per acceptance.
- [ ] **Step 3:** CI green → user squash-merges.

---

## Self-review notes

- Issue deliverables: LookupByIMDB + 404→ErrNotFound (T1), Episodes + blank air dates (T2), fixtures + httptest throughout; acceptance command runs in every task.
- Policy numbers appear once in Global Constraints and once in code — kept identical (600ms/burst 1, 10s, one retry, 500ms fallback).
- The `var _ = strconv.Itoa` placeholder in T1 keeps the import compiling before T2 uses it, and T2 explicitly deletes it — no dead code at branch end.
- Limiter in tests: T3's three tests make ≤2 requests each; the 600ms token wait adds ~600ms per second-request test — bounded, no flakiness vector (burst 1 grants the first token instantly, Wait is deterministic).
