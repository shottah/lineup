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
