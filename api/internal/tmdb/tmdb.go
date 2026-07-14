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
