# TMDB Client Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `api/internal/tmdb` — a fixture-tested TMDB v3 client (multi-search, movie/TV details, flatrate watch providers) authenticating with a v4 read access token, per issue #9 and the approved spec `docs/superpowers/specs/2026-07-14-tmdb-client-design.md`.

**Architecture:** One flat package mirroring `api/internal/tvmaze` (plain constructors, `get(ctx, path, out)` helper, `tmdb:`-prefixed wrapped errors, `ErrNotFound` sentinel, `x/time/rate` limiter, single retry on 429/5xx). Config and infra rename `TMDB_API_KEY`/`tmdb-api-key` → `TMDB_READ_TOKEN`/`tmdb-read-token`. Tests are hermetic httptest + recorded fixtures.

**Tech Stack:** Go (see `api/go.mod`), stdlib `net/http` + `encoding/json`, `golang.org/x/time/rate` (already a dependency — do NOT `go get` anything), `curl` + `jq` for one-time fixture recording.

## Global Constraints

- Branch: `feat/9-tmdb-client` (exists). All commits go there; squash-merge at the end.
- No new Go dependencies. No network in any test (`go test` must pass offline).
- The real TMDB read token must NEVER appear in any tracked file, commit, or command echoed into a file under the repo. It lives only in `~/.lineup/tmdb_read_token` (created by Matthew, `chmod 600`).
- Kind vocabulary is Lineup's: `"movie"` | `"series"` (never `"tv"` in exported surface).
- Error strings are prefixed `tmdb:`; sentinel `ErrNotFound` for 404s.
- All Go work happens under `api/` — run go commands with `cd api` or `-C api`.
- Before every commit: `gofmt -l ./internal/...` prints nothing, `go vet ./...` clean.
- **Infra commands (anything `gcloud`) are Matthew-only: show the command, explain it, wait for explicit approval, run one at a time. Agentic workers must NOT run Task 8's gcloud steps — hand back to the user.**

---

### Task 1: Config rename — `TMDBReadToken` / `TMDB_READ_TOKEN`

**Files:**
- Modify: `api/internal/config/config.go`
- Test: `api/internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `config.Config.TMDBReadToken string`, populated from env `TMDB_READ_TOKEN`, optional (empty when unset). Task 8 renames the infra side to match; nothing else reads this field yet (#11 will).

- [ ] **Step 1: Update the test to the new names (failing test)**

In `api/internal/config/config_test.go` make exactly these three replacements:

Replace:
```go
var envKeys = []string{"DATABASE_URL", "TMDB_API_KEY", "FIREBASE_PROJECT_ID", "PORT"}
```
with:
```go
var envKeys = []string{"DATABASE_URL", "TMDB_READ_TOKEN", "FIREBASE_PROJECT_ID", "PORT"}
```

Replace (inside the `"all fields set"` case's `env` map):
```go
				"TMDB_API_KEY":        "tmdb-key",
```
with:
```go
				"TMDB_READ_TOKEN":     "tmdb-token",
```

Replace (inside the same case's `want`):
```go
				TMDBKey:           "tmdb-key",
```
with:
```go
				TMDBReadToken:     "tmdb-token",
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/config/`
Expected: FAIL — compile error `unknown field TMDBReadToken in struct literal of type Config`.

- [ ] **Step 3: Rename in the implementation**

In `api/internal/config/config.go` make exactly these three replacements:

Replace:
```go
// (TMDBKey: task 9, Port: consumed directly by httpserver.New, which already
```
with:
```go
// (TMDBReadToken: consumed by ingestion (#11); Port: consumed directly by
// httpserver.New, which already
```

Replace:
```go
	TMDBKey           string
```
with:
```go
	TMDBReadToken     string
```

Replace:
```go
		TMDBKey:           os.Getenv("TMDB_API_KEY"),
```
with:
```go
		TMDBReadToken:     os.Getenv("TMDB_READ_TOKEN"),
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api && gofmt -l ./internal/config/ && go vet ./internal/config/ && go test ./internal/config/`
Expected: gofmt prints nothing; PASS.

- [ ] **Step 5: Commit**

```bash
git add api/internal/config/config.go api/internal/config/config_test.go
git commit -m "feat(api): config reads TMDB_READ_TOKEN for v4 bearer auth"
```

---

### Task 2: Record TMDB fixtures

**Files:**
- Create: `api/internal/tmdb/testdata/search_multi.json`
- Create: `api/internal/tmdb/testdata/movie_details.json`
- Create: `api/internal/tmdb/testdata/tv_details.json`
- Create: `api/internal/tmdb/testdata/tv_details_variant.json`
- Create: `api/internal/tmdb/testdata/watch_providers.json`

**Interfaces:**
- Consumes: Matthew's token at `~/.lineup/tmdb_read_token`. **If the file is missing, stop and ask the user to create it** (`mkdir -p ~/.lineup && printf '%s' '<token>' > ~/.lineup/tmdb_read_token && chmod 600 ~/.lineup/tmdb_read_token`, run by the user themselves so the token never enters the transcript).
- Produces: the five fixture files above, with the exact shapes/anchor values Tasks 3–6 assert (movie 603 "The Matrix" runtime 136 year 1999; tv 1399 "Game of Thrones" year 2011, imdb tt0944947, status Ended, 8 non-special seasons, season 1 = 10 episodes; a `person` entry present in the search fixture; US flatrate + at least one non-flatrate section in the providers fixture).

This task records real API responses once, at development time. Tests never touch the network.

- [ ] **Step 1: Pull raw responses into a scratch dir**

```bash
TOKEN=$(cat ~/.lineup/tmdb_read_token)
BASE=https://api.themoviedb.org/3
SCRATCH=$(mktemp -d)
curl -sf -H "Authorization: Bearer $TOKEN" "$BASE/search/multi?query=the%20matrix"    > "$SCRATCH/search_matrix.json"
curl -sf -H "Authorization: Bearer $TOKEN" "$BASE/search/multi?query=game%20of%20thrones" > "$SCRATCH/search_got.json"
curl -sf -H "Authorization: Bearer $TOKEN" "$BASE/search/multi?query=keanu%20reeves"  > "$SCRATCH/search_keanu.json"
curl -sf -H "Authorization: Bearer $TOKEN" "$BASE/movie/603"                          > "$SCRATCH/movie_603.json"
curl -sf -H "Authorization: Bearer $TOKEN" "$BASE/tv/1399?append_to_response=external_ids" > "$SCRATCH/tv_1399.json"
curl -sf -H "Authorization: Bearer $TOKEN" "$BASE/tv/1399/watch/providers"            > "$SCRATCH/wp_1399.json"
echo "scratch: $SCRATCH"
```

Expected: no output but the echo; every file non-empty. `curl -sf` fails loudly on HTTP errors — a 401 here means the token file content is wrong; stop and tell the user.

- [ ] **Step 2: Build the search fixture (one movie + one tv + one person, real shapes)**

```bash
mkdir -p api/internal/tmdb/testdata
jq -n \
  --argjson movie  "$(jq '[.results[] | select(.media_type=="movie"  and .id==603)][0]'  "$SCRATCH/search_matrix.json")" \
  --argjson tv     "$(jq '[.results[] | select(.media_type=="tv"     and .id==1399)][0]' "$SCRATCH/search_got.json")" \
  --argjson person "$(jq '[.results[] | select(.media_type=="person" and .id==6384)][0]' "$SCRATCH/search_keanu.json")" \
  '{page: 1, results: [$movie, $tv, $person], total_pages: 1, total_results: 3}' \
  > api/internal/tmdb/testdata/search_multi.json
jq -e '.results | length == 3 and (map(. != null) | all)' api/internal/tmdb/testdata/search_multi.json
jq -e '.results[0].release_date | startswith("1999")' api/internal/tmdb/testdata/search_multi.json
jq -e '.results[1].first_air_date | startswith("2011")' api/internal/tmdb/testdata/search_multi.json
```

Expected: `true` three times. If any anchor id (603 / 1399 / 6384) is missing from its page-1 results, print the ids present (`jq '.results | map({media_type, id, title, name})' <file>`) and stop — do not substitute a different title without telling the user, because Task 3's test asserts these exact ids.

- [ ] **Step 3: Save the detail fixtures and verify their anchor values**

```bash
cp "$SCRATCH/movie_603.json" api/internal/tmdb/testdata/movie_details.json
cp "$SCRATCH/tv_1399.json"   api/internal/tmdb/testdata/tv_details.json
jq -e '.id == 603 and .title == "The Matrix" and .runtime == 136' api/internal/tmdb/testdata/movie_details.json
jq -e '.id == 1399 and .external_ids.imdb_id == "tt0944947" and .status == "Ended"' api/internal/tmdb/testdata/tv_details.json
jq -e '[.seasons[].season_number] | index(0) != null' api/internal/tmdb/testdata/tv_details.json
jq -e '([.seasons[] | select(.season_number > 0)] | length) == 8' api/internal/tmdb/testdata/tv_details.json
jq -e '(.seasons[] | select(.season_number == 1) | .episode_count) == 10' api/internal/tmdb/testdata/tv_details.json
jq '.episode_run_time' api/internal/tmdb/testdata/tv_details.json
```

Expected: `true` five times, then the `episode_run_time` array — **note its first element (or that it is empty)**: Task 5's `gotRuntimeMins` constant is `60` and assumes `[60]`; if the printed array differs, Task 5 says exactly where to adjust. If any `-e` check fails, TMDB's data drifted from these historically stable values — stop and show the user the actual output.

- [ ] **Step 4: Derive the airing/no-IMDB variant**

```bash
jq '.status = "Returning Series" | .external_ids.imdb_id = null | .episode_run_time = []' \
  api/internal/tmdb/testdata/tv_details.json > api/internal/tmdb/testdata/tv_details_variant.json
jq -e '.status == "Returning Series" and .external_ids.imdb_id == null and .episode_run_time == []' \
  api/internal/tmdb/testdata/tv_details_variant.json
```

Expected: `true`. (A hand-edited variant of a recorded fixture, per the spec — it pins the `Airing`, empty-`IMDBID`, and zero-runtime decode paths.)

- [ ] **Step 5: Trim the providers fixture to the US entry, keeping non-flatrate sections**

```bash
jq '{id, results: {US: .results.US}}' "$SCRATCH/wp_1399.json" > api/internal/tmdb/testdata/watch_providers.json
jq -e '.results.US.flatrate | length > 0' api/internal/tmdb/testdata/watch_providers.json
jq -e '.results.US | (has("buy") or has("rent"))' api/internal/tmdb/testdata/watch_providers.json
```

Expected: `true` twice. The buy/rent sections stay in the fixture so Task 6's test proves they are *ignored* (flatrate-only), and the test derives its expected list from the fixture's own `flatrate` array, so exact provider ids don't need pinning here.

- [ ] **Step 6: Confirm no secrets leaked into the fixtures, then commit**

```bash
grep -ri "$(cat ~/.lineup/tmdb_read_token)" api/internal/tmdb/testdata/ && echo "TOKEN LEAKED - DO NOT COMMIT" || echo clean
git add api/internal/tmdb/testdata/
git commit -m "test(api): record tmdb fixtures (search, details, providers)"
```

Expected: `clean`, then the commit.

---

### Task 3: Client core + `SearchMulti` + transport behavior

**Files:**
- Create: `api/internal/tmdb/tmdb.go`
- Test: `api/internal/tmdb/tmdb_test.go`

**Interfaces:**
- Consumes: `testdata/search_multi.json` (Task 2).
- Produces (used by Tasks 4–6 and by #11's ingestion):
  - `tmdb.New(readToken string) *Client`, `tmdb.NewWithBaseURL(base, readToken string) *Client`
  - `tmdb.ErrNotFound` (sentinel `error`)
  - `(*Client).SearchMulti(ctx context.Context, query string) ([]SearchResult, error)`
  - `tmdb.SearchResult{TMDBID int64; Kind, Name, Overview, PosterPath, Year string}`
  - unexported `(*Client).get(ctx context.Context, path string, out any) error` — Bearer auth, rate limit, single retry on 429/5xx, 404→ErrNotFound (later methods call it)

- [ ] **Step 1: Write the failing tests**

Create `api/internal/tmdb/tmdb_test.go`:

```go
package tmdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

func TestSearchMulti(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "search_multi.json"))
	}))
	defer srv.Close()

	c := NewWithBaseURL(srv.URL, "test-token")
	results, err := c.SearchMulti(context.Background(), "the matrix")
	if err != nil {
		t.Fatalf("SearchMulti: %v", err)
	}
	if gotPath != "/3/search/multi?query=the+matrix" {
		t.Fatalf("requested %q, want /3/search/multi?query=the+matrix", gotPath)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("Authorization = %q, want Bearer test-token", gotAuth)
	}
	// Fixture holds one movie (603), one tv (1399), one person; the person
	// must be dropped and tv mapped to "series".
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 (person dropped): %+v", len(results), results)
	}
	movie, series := results[0], results[1]
	if movie.Kind != "movie" || movie.TMDBID != 603 || movie.Name != "The Matrix" || movie.Year != "1999" {
		t.Fatalf("movie = %+v, want movie/603/The Matrix/1999", movie)
	}
	if movie.Overview == "" || movie.PosterPath == "" {
		t.Fatalf("movie overview/poster not decoded: %+v", movie)
	}
	if series.Kind != "series" || series.TMDBID != 1399 || series.Name != "Game of Thrones" || series.Year != "2011" {
		t.Fatalf("series = %+v, want series/1399/Game of Thrones/2011", series)
	}
}

func TestNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"status_code":34,"status_message":"not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewWithBaseURL(srv.URL, "test-token")
	if _, err := c.SearchMulti(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRetryOn429ThenSuccess(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"page":1,"results":[]}`))
	}))
	defer srv.Close()

	c := NewWithBaseURL(srv.URL, "test-token")
	results, err := c.SearchMulti(context.Background(), "q")
	if err != nil {
		t.Fatalf("SearchMulti after retry: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (exactly one retry)", calls)
	}
	if len(results) != 0 {
		t.Fatalf("results = %+v, want empty", results)
	}
}

func TestRetryCapOn500(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewWithBaseURL(srv.URL, "test-token")
	_, err := c.SearchMulti(context.Background(), "q")
	if err == nil || !strings.Contains(err.Error(), "unexpected status 500") {
		t.Fatalf("err = %v, want unexpected status 500", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (retry capped at one)", calls)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd api && go test ./internal/tmdb/`
Expected: FAIL — compile errors (`undefined: NewWithBaseURL`, `undefined: ErrNotFound`).

- [ ] **Step 3: Implement the client core and SearchMulti**

Create `api/internal/tmdb/tmdb.go`:

```go
// Package tmdb is a minimal client for the TMDB v3 API: multi-search,
// movie/TV details, and per-region flatrate watch providers. Requests
// authenticate with a v4 read access token (Bearer header), are
// rate-limited well under TMDB's ~50 req/s guideline, and are retried
// once on 429/5xx.
package tmdb

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

const defaultBaseURL = "https://api.themoviedb.org"

// ErrNotFound is returned when TMDB has no entry for the requested
// resource.
var ErrNotFound = errors.New("tmdb: not found")

// SearchResult is one multi-search hit Lineup consumes. Kind is Lineup's
// vocabulary ("movie"|"series" — TMDB's "tv" is mapped); person results
// are dropped before they reach callers. Year is "2019"-style or ""
// when TMDB has no date, for search-UI disambiguation.
type SearchResult struct {
	TMDBID     int64
	Kind       string
	Name       string
	Overview   string
	PosterPath string
	Year       string
}

// Client talks to TMDB with a bounded timeout, a token-bucket rate limit
// under TMDB's guideline, and a single retry on 429/5xx.
type Client struct {
	baseURL   string
	readToken string
	http      *http.Client
	limiter   *rate.Limiter
}

// New returns a production Client against api.themoviedb.org,
// authenticating with a v4 read access token.
func New(readToken string) *Client {
	return NewWithBaseURL(defaultBaseURL, readToken)
}

// NewWithBaseURL returns a Client against base — the test seam for
// httptest servers.
func NewWithBaseURL(base, readToken string) *Client {
	return &Client{
		baseURL:   base,
		readToken: readToken,
		http:      &http.Client{Timeout: 10 * time.Second},
		limiter:   rate.NewLimiter(rate.Limit(20), 5),
	}
}

// searchResult is the wire shape of one /search/multi hit; movies carry
// title/release_date, tv carries name/first_air_date.
type searchResult struct {
	MediaType    string `json:"media_type"`
	ID           int64  `json:"id"`
	Title        string `json:"title"`
	Name         string `json:"name"`
	Overview     string `json:"overview"`
	PosterPath   string `json:"poster_path"`
	ReleaseDate  string `json:"release_date"`
	FirstAirDate string `json:"first_air_date"`
}

// SearchMulti proxies GET /3/search/multi (first page only — a thin v1
// search needs no pagination), keeping movie/tv results and mapping tv
// to Lineup's "series".
func (c *Client) SearchMulti(ctx context.Context, query string) ([]SearchResult, error) {
	var resp struct {
		Results []searchResult `json:"results"`
	}
	path := "/3/search/multi?query=" + url.QueryEscape(query)
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(resp.Results))
	for _, r := range resp.Results {
		sr := SearchResult{TMDBID: r.ID, Overview: r.Overview, PosterPath: r.PosterPath}
		switch r.MediaType {
		case "movie":
			sr.Kind = "movie"
			sr.Name = r.Title
			sr.Year = yearOf(r.ReleaseDate)
		case "tv":
			sr.Kind = "series"
			sr.Name = r.Name
			sr.Year = yearOf(r.FirstAirDate)
		default:
			continue
		}
		out = append(out, sr)
	}
	return out, nil
}

// yearOf extracts "YYYY" from a "YYYY-MM-DD" date, "" when absent.
func yearOf(date string) string {
	if len(date) < 4 {
		return ""
	}
	return date[:4]
}

// get performs a rate-limited, Bearer-authenticated GET with a single
// retry on 429/5xx (honoring numeric Retry-After seconds, clamped at
// 30s, else 500ms), decoding a 200 JSON body into out. 404 maps to
// ErrNotFound and never retries.
func (c *Client) get(ctx context.Context, path string, out any) error {
	const maxAttempts = 2
	for attempt := 1; ; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return fmt.Errorf("tmdb: rate limit wait: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
		if err != nil {
			return fmt.Errorf("tmdb: new request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.readToken)
		res, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("tmdb: %s: %w", path, err)
		}

		retryable := res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500
		if retryable && attempt < maxAttempts {
			// Retry-After's HTTP-date form is deliberately unsupported:
			// non-numeric values fall back to the 500ms default, and the
			// delay is clamped so a misbehaving server can't stall callers.
			delay := 500 * time.Millisecond
			if s := res.Header.Get("Retry-After"); s != "" {
				if secs, perr := strconv.Atoi(s); perr == nil && secs >= 0 {
					if secs > 30 {
						secs = 30
					}
					delay = time.Duration(secs) * time.Second
				}
			}
			res.Body.Close()
			select {
			case <-ctx.Done():
				return fmt.Errorf("tmdb: %s: %w", path, ctx.Err())
			case <-time.After(delay):
			}
			continue
		}

		defer res.Body.Close()
		switch {
		case res.StatusCode == http.StatusNotFound:
			return ErrNotFound
		case res.StatusCode != http.StatusOK:
			return fmt.Errorf("tmdb: %s: unexpected status %d", path, res.StatusCode)
		}
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			return fmt.Errorf("tmdb: %s: decode: %w", path, err)
		}
		return nil
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api && gofmt -l ./internal/tmdb/ && go vet ./internal/tmdb/ && go test ./internal/tmdb/ -v`
Expected: gofmt prints nothing; all four tests PASS. `TestRetryOn429ThenSuccess` must finish fast (Retry-After: 0) — a multi-second run means the delay logic is wrong.

- [ ] **Step 5: Commit**

```bash
git add api/internal/tmdb/tmdb.go api/internal/tmdb/tmdb_test.go
git commit -m "feat(api): tmdb client core with bearer auth and multi-search"
```

---

### Task 4: `MovieDetails`

**Files:**
- Modify: `api/internal/tmdb/tmdb.go` (append)
- Test: `api/internal/tmdb/tmdb_test.go` (append)

**Interfaces:**
- Consumes: `(*Client).get` (Task 3), `testdata/movie_details.json` (Task 2).
- Produces: `(*Client).MovieDetails(ctx context.Context, id int64) (Movie, error)`; `tmdb.Movie{TMDBID int64; Name, Overview, PosterPath string; RuntimeMinutes int}`.

- [ ] **Step 1: Write the failing test (append to `tmdb_test.go`)**

```go
func TestMovieDetails(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "movie_details.json"))
	}))
	defer srv.Close()

	c := NewWithBaseURL(srv.URL, "test-token")
	m, err := c.MovieDetails(context.Background(), 603)
	if err != nil {
		t.Fatalf("MovieDetails: %v", err)
	}
	if gotPath != "/3/movie/603" {
		t.Fatalf("requested %q, want /3/movie/603", gotPath)
	}
	if m.TMDBID != 603 || m.Name != "The Matrix" || m.RuntimeMinutes != 136 {
		t.Fatalf("movie = %+v, want 603/The Matrix/136m", m)
	}
	if m.Overview == "" || m.PosterPath == "" {
		t.Fatalf("overview/poster not decoded: %+v", m)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd api && go test ./internal/tmdb/ -run TestMovieDetails`
Expected: FAIL — compile error `c.MovieDetails undefined`.

- [ ] **Step 3: Implement (append to `tmdb.go`)**

```go
// Movie is the subset of TMDB movie details the titles table consumes.
type Movie struct {
	TMDBID         int64
	Name           string
	Overview       string
	PosterPath     string
	RuntimeMinutes int
}

// MovieDetails fetches GET /3/movie/{id}.
func (c *Client) MovieDetails(ctx context.Context, id int64) (Movie, error) {
	var resp struct {
		ID         int64  `json:"id"`
		Title      string `json:"title"`
		Overview   string `json:"overview"`
		PosterPath string `json:"poster_path"`
		Runtime    int    `json:"runtime"`
	}
	if err := c.get(ctx, "/3/movie/"+strconv.FormatInt(id, 10), &resp); err != nil {
		return Movie{}, err
	}
	return Movie{
		TMDBID:         resp.ID,
		Name:           resp.Title,
		Overview:       resp.Overview,
		PosterPath:     resp.PosterPath,
		RuntimeMinutes: resp.Runtime,
	}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api && gofmt -l ./internal/tmdb/ && go vet ./internal/tmdb/ && go test ./internal/tmdb/`
Expected: gofmt prints nothing; PASS (all tests, not just the new one).

- [ ] **Step 5: Commit**

```bash
git add api/internal/tmdb/tmdb.go api/internal/tmdb/tmdb_test.go
git commit -m "feat(api): tmdb movie details"
```

---

### Task 5: `TVDetails`

**Files:**
- Modify: `api/internal/tmdb/tmdb.go` (append)
- Test: `api/internal/tmdb/tmdb_test.go` (append)

**Interfaces:**
- Consumes: `(*Client).get` (Task 3), `testdata/tv_details.json` + `testdata/tv_details_variant.json` (Task 2).
- Produces: `(*Client).TVDetails(ctx context.Context, id int64) (TV, error)`; `tmdb.TV{TMDBID int64; Name, Overview, PosterPath string; RuntimeMinutes int; Airing bool; IMDBID string; Seasons []Season}`; `tmdb.Season{Number, EpisodeCount int}`.

- [ ] **Step 1: Write the failing tests (append to `tmdb_test.go`)**

```go
// gotRuntimeMins is Game of Thrones' first episode_run_time entry as
// recorded in tv_details.json. If Task 2's recording printed a different
// episode_run_time array (TMDB data drifts), set this to its first
// element — or 0 if the array was empty.
const gotRuntimeMins = 60

func TestTVDetails(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "tv_details.json"))
	}))
	defer srv.Close()

	c := NewWithBaseURL(srv.URL, "test-token")
	tv, err := c.TVDetails(context.Background(), 1399)
	if err != nil {
		t.Fatalf("TVDetails: %v", err)
	}
	if gotPath != "/3/tv/1399?append_to_response=external_ids" {
		t.Fatalf("requested %q, want /3/tv/1399?append_to_response=external_ids", gotPath)
	}
	if tv.TMDBID != 1399 || tv.Name != "Game of Thrones" {
		t.Fatalf("tv = %+v, want 1399/Game of Thrones", tv)
	}
	if tv.IMDBID != "tt0944947" {
		t.Fatalf("IMDBID = %q, want tt0944947", tv.IMDBID)
	}
	if tv.Airing {
		t.Fatalf("Airing = true for status Ended, want false")
	}
	if tv.RuntimeMinutes != gotRuntimeMins {
		t.Fatalf("RuntimeMinutes = %d, want %d", tv.RuntimeMinutes, gotRuntimeMins)
	}
	// The fixture contains season 0 (specials) plus seasons 1-8; season 0
	// must be excluded and season 1's episode count preserved.
	if len(tv.Seasons) != 8 {
		t.Fatalf("got %d seasons, want 8 (specials excluded): %+v", len(tv.Seasons), tv.Seasons)
	}
	for _, s := range tv.Seasons {
		if s.Number == 0 {
			t.Fatalf("season 0 not excluded: %+v", tv.Seasons)
		}
	}
	if tv.Seasons[0].Number != 1 || tv.Seasons[0].EpisodeCount != 10 {
		t.Fatalf("season 1 = %+v, want number 1 / 10 episodes", tv.Seasons[0])
	}
}

func TestTVDetailsAiringWithoutIMDB(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "tv_details_variant.json"))
	}))
	defer srv.Close()

	c := NewWithBaseURL(srv.URL, "test-token")
	tv, err := c.TVDetails(context.Background(), 1399)
	if err != nil {
		t.Fatalf("TVDetails: %v", err)
	}
	if !tv.Airing {
		t.Fatalf("Airing = false for status Returning Series, want true")
	}
	if tv.IMDBID != "" {
		t.Fatalf("IMDBID = %q for null external imdb_id, want empty", tv.IMDBID)
	}
	if tv.RuntimeMinutes != 0 {
		t.Fatalf("RuntimeMinutes = %d for empty episode_run_time, want 0", tv.RuntimeMinutes)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd api && go test ./internal/tmdb/ -run TestTVDetails`
Expected: FAIL — compile error `c.TVDetails undefined`.

- [ ] **Step 3: Implement (append to `tmdb.go`)**

```go
// Season is one non-specials season; EpisodeCount drives the rotation
// episode pointer.
type Season struct {
	Number       int
	EpisodeCount int
}

// TV is the subset of TMDB TV details the titles/title_seasons tables
// consume. IMDBID is "" when TMDB has none — a normal outcome ingestion
// branches on (skips the TVMaze lookup), not an error.
type TV struct {
	TMDBID         int64
	Name           string
	Overview       string
	PosterPath     string
	RuntimeMinutes int
	Airing         bool
	IMDBID         string
	Seasons        []Season
}

// TVDetails fetches GET /3/tv/{id} with external_ids appended (one round
// trip). Season 0 (specials) is excluded; RuntimeMinutes is the first
// episode_run_time entry, 0 when TMDB reports none; Airing means
// status == "Returning Series".
func (c *Client) TVDetails(ctx context.Context, id int64) (TV, error) {
	var resp struct {
		ID             int64  `json:"id"`
		Name           string `json:"name"`
		Overview       string `json:"overview"`
		PosterPath     string `json:"poster_path"`
		EpisodeRunTime []int  `json:"episode_run_time"`
		Status         string `json:"status"`
		Seasons        []struct {
			SeasonNumber int `json:"season_number"`
			EpisodeCount int `json:"episode_count"`
		} `json:"seasons"`
		ExternalIDs struct {
			IMDBID string `json:"imdb_id"`
		} `json:"external_ids"`
	}
	path := "/3/tv/" + strconv.FormatInt(id, 10) + "?append_to_response=external_ids"
	if err := c.get(ctx, path, &resp); err != nil {
		return TV{}, err
	}
	tv := TV{
		TMDBID:     resp.ID,
		Name:       resp.Name,
		Overview:   resp.Overview,
		PosterPath: resp.PosterPath,
		Airing:     resp.Status == "Returning Series",
		IMDBID:     resp.ExternalIDs.IMDBID,
	}
	if len(resp.EpisodeRunTime) > 0 {
		tv.RuntimeMinutes = resp.EpisodeRunTime[0]
	}
	for _, s := range resp.Seasons {
		if s.SeasonNumber == 0 {
			continue
		}
		tv.Seasons = append(tv.Seasons, Season{Number: s.SeasonNumber, EpisodeCount: s.EpisodeCount})
	}
	return tv, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api && gofmt -l ./internal/tmdb/ && go vet ./internal/tmdb/ && go test ./internal/tmdb/`
Expected: gofmt prints nothing; PASS. If only the `RuntimeMinutes` assertion fails, the fixture's `episode_run_time` differs from `[60]` — fix the `gotRuntimeMins` constant (see its comment), nothing else.

- [ ] **Step 5: Commit**

```bash
git add api/internal/tmdb/tmdb.go api/internal/tmdb/tmdb_test.go
git commit -m "feat(api): tmdb tv details with external ids and season filtering"
```

---

### Task 6: `WatchProviders`

**Files:**
- Modify: `api/internal/tmdb/tmdb.go` (append)
- Test: `api/internal/tmdb/tmdb_test.go` (append; adds `encoding/json` import)

**Interfaces:**
- Consumes: `(*Client).get` (Task 3), `testdata/watch_providers.json` (Task 2).
- Produces: `(*Client).WatchProviders(ctx context.Context, kind string, id int64, region string) ([]Provider, error)`; `tmdb.Provider{ID int64; Name, LogoPath string}`. Kind is `"movie"`|`"series"`; anything else errors without a request. Missing region / no flatrate → empty slice, nil error.

- [ ] **Step 1: Write the failing tests (append to `tmdb_test.go`, and add `"encoding/json"` to its imports)**

```go
func TestWatchProviders(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "watch_providers.json"))
	}))
	defer srv.Close()

	c := NewWithBaseURL(srv.URL, "test-token")
	got, err := c.WatchProviders(context.Background(), "series", 1399, "US")
	if err != nil {
		t.Fatalf("WatchProviders: %v", err)
	}
	if gotPath != "/3/tv/1399/watch/providers" {
		t.Fatalf("requested %q, want /3/tv/1399/watch/providers (series maps to tv)", gotPath)
	}

	// Expected set derives from the fixture's own US.flatrate array; the
	// fixture also carries buy/rent sections, which must NOT leak through.
	var raw struct {
		Results map[string]struct {
			Flatrate []struct {
				ProviderID int64  `json:"provider_id"`
				Name       string `json:"provider_name"`
				LogoPath   string `json:"logo_path"`
			} `json:"flatrate"`
		} `json:"results"`
	}
	if err := json.Unmarshal(fixture(t, "watch_providers.json"), &raw); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	want := raw.Results["US"].Flatrate
	if len(want) == 0 {
		t.Fatal("fixture has no US flatrate entries; re-record Task 2")
	}
	if len(got) != len(want) {
		t.Fatalf("got %d providers, want %d (flatrate only): %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].ID != w.ProviderID || got[i].Name != w.Name || got[i].LogoPath != w.LogoPath {
			t.Fatalf("provider[%d] = %+v, want %+v", i, got[i], w)
		}
	}
}

func TestWatchProvidersRegionMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "watch_providers.json"))
	}))
	defer srv.Close()

	c := NewWithBaseURL(srv.URL, "test-token")
	got, err := c.WatchProviders(context.Background(), "series", 1399, "ZZ")
	if err != nil {
		t.Fatalf("WatchProviders(ZZ): %v, want nil error (no home region is data, not failure)", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v for absent region, want empty", got)
	}
}

func TestWatchProvidersBadKind(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		calls++
	}))
	defer srv.Close()

	c := NewWithBaseURL(srv.URL, "test-token")
	if _, err := c.WatchProviders(context.Background(), "tv", 1399, "US"); err == nil {
		t.Fatal("kind \"tv\" accepted, want error (Lineup vocabulary is movie|series)")
	}
	if calls != 0 {
		t.Fatalf("bad kind reached the server (%d calls), want validation before request", calls)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd api && go test ./internal/tmdb/ -run TestWatchProviders`
Expected: FAIL — compile error `c.WatchProviders undefined`.

- [ ] **Step 3: Implement (append to `tmdb.go`)**

```go
// Provider is one streaming provider; fields feed the providers table
// verbatim.
type Provider struct {
	ID       int64
	Name     string
	LogoPath string
}

// WatchProviders fetches GET /3/{movie|tv}/{id}/watch/providers and
// returns the region's flatrate (streaming) entries only — rent/buy are
// ignored. kind is Lineup's "movie"|"series". A region TMDB doesn't list,
// or one without flatrate offers, yields an empty slice: a title having
// no streaming home somewhere is data, not failure.
func (c *Client) WatchProviders(ctx context.Context, kind string, id int64, region string) ([]Provider, error) {
	var segment string
	switch kind {
	case "movie":
		segment = "movie"
	case "series":
		segment = "tv"
	default:
		return nil, fmt.Errorf("tmdb: watch providers: unknown kind %q", kind)
	}
	var resp struct {
		Results map[string]struct {
			Flatrate []struct {
				ProviderID int64  `json:"provider_id"`
				Name       string `json:"provider_name"`
				LogoPath   string `json:"logo_path"`
			} `json:"flatrate"`
		} `json:"results"`
	}
	path := "/3/" + segment + "/" + strconv.FormatInt(id, 10) + "/watch/providers"
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, err
	}
	out := []Provider{}
	for _, p := range resp.Results[region].Flatrate {
		out = append(out, Provider{ID: p.ProviderID, Name: p.Name, LogoPath: p.LogoPath})
	}
	return out, nil
}
```

- [ ] **Step 4: Run the full package plus vet/fmt**

Run: `cd api && gofmt -l ./internal/tmdb/ && go vet ./internal/tmdb/ && go test ./internal/tmdb/ -v`
Expected: gofmt prints nothing; all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add api/internal/tmdb/tmdb.go api/internal/tmdb/tmdb_test.go
git commit -m "feat(api): tmdb flatrate watch providers"
```

---

### Task 7: Infra rename — `tmdb-read-token`

**Files:**
- Modify: `infra/run-service.yaml` (lines ~35-39)
- Modify: `infra/bootstrap.sh` (lines 16-17)
- Modify: `infra/README.md` (the "Replace the placeholder TMDB API key" checklist item, ~line 115)

No gcloud commands in this task — file edits only. The cloud-side secret is Task 8 (Matthew, gated).

**Interfaces:**
- Consumes: Task 1's env name `TMDB_READ_TOKEN`.
- Produces: deploy manifests and bootstrap script consistent with it; Task 8 creates the actual secret before merge.

- [ ] **Step 1: Rename in `infra/run-service.yaml`**

Replace:
```yaml
            - name: TMDB_API_KEY
              valueFrom:
                secretKeyRef:
                  name: tmdb-api-key
                  key: "latest"
```
with:
```yaml
            - name: TMDB_READ_TOKEN
              valueFrom:
                secretKeyRef:
                  name: tmdb-read-token
                  key: "latest"
```

- [ ] **Step 2: Rename in `infra/bootstrap.sh`**

Replace:
```bash
gcloud secrets describe tmdb-api-key --project "$PROJECT_ID" >/dev/null 2>&1 || \
  (printf 'REPLACE_ME' | gcloud secrets create tmdb-api-key --data-file=- --project "$PROJECT_ID")
```
with:
```bash
gcloud secrets describe tmdb-read-token --project "$PROJECT_ID" >/dev/null 2>&1 || \
  (printf 'REPLACE_ME' | gcloud secrets create tmdb-read-token --data-file=- --project "$PROJECT_ID")
```

- [ ] **Step 3: Update the `infra/README.md` checklist item**

Replace:
```markdown
- [ ] **Replace the placeholder TMDB API key** with the real value:

  ```bash
  printf '%s' 'YOUR_REAL_TMDB_KEY' | gcloud secrets versions add tmdb-api-key --data-file=- --project lineup-app-ae6b
  ```
```
with:
```markdown
- [ ] **Replace the placeholder TMDB read token** (v4 read access token, sent
  as a Bearer header — not the v3 api key) with the real value:

  ```bash
  printf '%s' "$(cat ~/.lineup/tmdb_read_token)" | gcloud secrets versions add tmdb-read-token --data-file=- --project lineup-app-ae6b
  ```
```

- [ ] **Step 4: Verify nothing still references the old names, and the whole repo tests green**

Run: `grep -rn "TMDB_API_KEY\|tmdb-api-key\|TMDBKey" api/ infra/ web/ .github/ firebase.json ; cd api && go test ./...`
Expected: grep finds nothing (exit 1) — `docs/` is deliberately excluded, the spec records the old names as history; all api packages PASS.

- [ ] **Step 5: Commit**

```bash
git add infra/run-service.yaml infra/bootstrap.sh infra/README.md
git commit -m "chore(infra): rename tmdb secret to tmdb-read-token"
```

---

### Task 8: Pre-merge secret, PR, squash-merge — **Matthew-gated**

**Files:** none (cloud state + GitHub).

**Interfaces:**
- Consumes: everything above, pushed on `feat/9-tmdb-client`.
- Produces: issue #9 closed; `main` deploys with the `tmdb-read-token` secret resolvable.

**Agentic workers: complete Step 1 (push + PR), then STOP and hand back to the user.** Steps 2-5 touch cloud state: per Matthew's standing instruction, each gcloud command is shown, explained, and run only after his explicit approval, one at a time.

- [ ] **Step 1: Push and open the PR** (use the writing-github-content skill for the description)

```bash
git push -u origin feat/9-tmdb-client
gh pr create --title "feat(api): tmdb client" --body "..."   # body: closes #9, summary, test notes
```

- [ ] **Step 2 (Matthew, before merge): create the secret with the real token**

```bash
printf '%s' "$(cat ~/.lineup/tmdb_read_token)" | gcloud secrets create tmdb-read-token --data-file=- --project lineup-app-ae6b
```

Why before merge: the squash-merge touches `api/**`, triggering Cloud Build → Cloud Deploy, which applies `run-service.yaml`'s `secretKeyRef: tmdb-read-token`; if the secret doesn't exist, the new revision fails to start. No IAM step is needed — `api-runtime` holds project-level `secretmanager.secretAccessor` (bootstrap.sh).

- [ ] **Step 3 (Matthew): squash-merge the PR once CI is green**, closing #9.

- [ ] **Step 4 (Matthew): verify the rollout**

```bash
gcloud run services describe api --region us-central1 --project lineup-app-ae6b --format='value(status.conditions[0].status,status.latestReadyRevisionName)'
```

Expected: `True` and a new revision name. (Read-only; note the standing caveat that the Cloud Run TCP-edge wedge from the project restore is untested — if the deploy behaves strangely, consult the project memory before re-diagnosing.)

- [ ] **Step 5 (Matthew, after a healthy rollout): delete the orphaned placeholder secret**

```bash
gcloud secrets delete tmdb-api-key --project lineup-app-ae6b -q
```

---

## Execution notes

- Task order is strict: 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8. Tasks 4-6 each depend on Task 3's `get` and Task 2's fixtures.
- Task 2 requires the token file `~/.lineup/tmdb_read_token`; if absent, stop and ask the user before doing anything else.
- The only post-recording adjustable value is `gotRuntimeMins` (Task 5) — everything else asserts stable anchors or derives expectations from the fixture itself.
