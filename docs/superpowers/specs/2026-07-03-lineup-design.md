# Lineup — v1 Design

**Date:** 2026-07-03
**Status:** Approved

## Overview

Lineup brings back TV-guide-style viewing. Users build a profile of shows and movies (watched, watchlist, ratings, favorites), promote titles into an active **rotation**, and generate a personal **TV guide** for the week — a scheduled, appointment-television plan that gently structures viewing instead of feeding binges. The anti-binge motivation shapes the design but is not the marketing pitch; the pitch is *"your week of TV, planned like a lineup."*

## Goals (v1)

- Google/Apple sign-in.
- Discovery: search movies + series, view details and streaming availability, manage watchlist/watched/ratings/favorites.
- Rotation: a small set of actively-watched titles that feed the guide.
- Guide generation for the next 7 days (or custom range), honoring user schedule preferences.
- Two renderings of one guide: **calendar view** (default, single curated plan) and **guide-board view** (per-service rows with alternates).
- Guide editing: swap, move, delete, pin, regenerate-remaining, mark-watched.
- Zero paid third-party services; near-zero fixed infra cost.

## Non-Goals (v1) — roadmap, schema-anticipated

- Social layer: friends, activity feeds, shared lists.
- Google Calendar import/sync.
- Shared/collaborative guides (family movie night) — `guide_members` join table planned, not built.
- Notifications, native mobile apps.

## Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Name | **Lineup** | Broadcast vocabulary for scheduled programming; reads as a verb. Working title pending trademark/domain diligence. |
| Metadata | **TMDB + TVMaze** (both free) | TMDB: search, artwork, runtimes, per-region watch providers (JustWatch-powered). TVMaze: real episode air dates for currently-airing series. Attribution for both required in footer. |
| Backend | **Go REST API** (chi) on Cloud Run | Small API surface; REST simpler to build/cache/secure than GraphQL. GraphQL can be layered on the same storage for the v2 social layer if needed. |
| Database | **Postgres 16 on GCP always-free e2-micro VM** | $0 fixed cost, stays in GCP. Vanilla Postgres → trivial migration to Cloud SQL at traction. Ops burden mitigated by scripted setup + nightly GCS backups. |
| Frontend | **Next.js (App Router, TS) + Tailwind on Firebase App Hosting** | Matches user's existing App Hosting deployments; monorepo `rootDir: web`. |
| Auth | **Firebase Auth** (Google + Apple providers) | Free, meets the Google/Apple requirement; Go verifies ID tokens via Admin SDK. |
| GCP | **New dedicated project** | Isolates billing, IAM, and the Firebase Auth user pool from Strata infra. |
| API deploys | **Cloud Build → Artifact Registry → Cloud Deploy → Cloud Run** | Per explicit requirement. |

## Product Spec

### Platform & auth

Responsive web app. Sign in with Google or Apple (Firebase Auth client SDK). Apple sign-in requires Apple Developer configuration — tracked as a manual setup step; Google-only is acceptable at first launch if Apple setup lags.

### Discovery & profile

- Search across movies and series (TMDB multi-search, proxied server-side).
- Title page: poster, overview, runtime / episode info, streaming providers for the user's region (default `US`, changeable in settings).
- Actions per title: add to watchlist, mark watched, rate (0.5–5.0 in half-star steps), favorite.
- Profile shelves: Watchlist, In Rotation, Watched, Favorites, Ratings.

### Rotation

Users promote watchlist titles into the **rotation** — the actively-watched set the guide schedules from. Cap: **8 titles** (keeps weekly guides coherent). Each rotation series carries an **episode pointer** (season/episode up next); marking a guide item watched advances it using cached season/episode counts. When the pointer passes the final episode, the title auto-moves to Watched and leaves the rotation.

### Schedule preferences

Per weekday: enabled/disabled + viewing window (default 19:00–23:00). Movies are only scheduled on days whose window fits their runtime. Max one episode per series per day.

### The guide

Generating a guide (default: next 7 days) produces **guide items**: date, start/end time, title, season/episode (series), streaming service. Items are **plan** items (the curated pick) or **alternates**.

- **Calendar view (default — the anti-binge mode):** columns = days, rows = times; plan items only. One linear path through the week; the user switches *series*, never deliberates *services*.
- **Guide-board view:** rows = streaming services, columns = times, one day at a time; plan items + alternates — e.g., three different 8pm options across HBO/Amazon/Apple. Alternates draw from the full watchlist when rotation can't fill the board.

### Editing

- Swap an item's title (picker from rotation/watchlist), move to another slot/day, delete.
- Pin a series to a weekly night (e.g., HBO Sundays).
- **Regenerate remaining:** re-plans only future, unpinned, unedited items.
- Mark watched from the guide → advances the episode pointer.

## Guide Generation Engine

Deterministic greedy scheduler, seeded per guide (`guides.seed`): same inputs → same output, so regeneration is stable and testable.

**Inputs:** rotation entries (kind, runtime, providers, episode pointer, TVMaze air dates if airing), schedule prefs, date range, existing pinned/edited items (on regenerate).

**Hard constraints:**
1. Items fit entirely within the day's viewing window.
2. ≤ 1 episode per series per day.
3. Currently-airing series are never scheduled before an episode's real air date; when an air date falls in range, that episode is pinned to its air night (appointment TV).
4. A title is only placed on a service that actually carries it in the user's region.

**Soft constraints (scoring, applied greedily per slot):**
- **Fairness:** round-robin so every rotation title appears ≥ 1×/week when capacity allows.
- **Variety:** penalize the same series on consecutive nights.
- **Service cohesion:** a single night's plan prefers 1–2 services, so switching series isn't confounded with switching apps.
- **Movie placement:** prefer the longest-window days (weekends).

**Alternates pass:** after the plan is fixed, each plan timeslot gets alternates — other rotation/watchlist titles available on *other* services at that time — for the guide-board view.

## Data Model (Postgres)

- `users` — `firebase_uid` (unique), email, display_name, region (default `US`), `schedule_prefs` JSONB, timestamps.
- `titles` — `tmdb_id`, `tvmaze_id` (series, nullable), `kind` (movie|series), name, overview, poster_path, runtime_minutes, airing status, `refreshed_at`. Ingested on first user contact; refreshed on TTL (metadata ~30d, providers ~7d, airings ~1d).
- `title_seasons` — (title, season_number, episode_count) — drives pointer advancement.
- `title_airings` — (title, season, episode, air_date) for currently-airing series (TVMaze).
- `providers` — TMDB provider id, name, logo.
- `title_providers` — (title, region, provider) availability cache.
- `user_titles` — unique (user, title): `status` (`watchlist`|`rotation`|`watched`|`none`), `rating` numeric(2,1) nullable, `favorite` bool, `pointer_season`/`pointer_episode`, timestamps. One row serves all shelves.
- `guides` — user, start_date, end_date, seed, created_at.
- `guide_items` — guide, date, start_time, end_time, title, season/episode (nullable), provider, `is_plan`, `pinned`, `edited`, `watched`.

Migrations: `golang-migrate`, embedded and run at API startup (advisory-locked, safe across concurrent Cloud Run instances).

## REST API (Go, `/v1`)

Auth: `Authorization: Bearer <Firebase ID token>`, verified by middleware via Firebase Admin SDK; user row upserted on first authenticated request.

```
GET    /v1/me                                    profile + schedule prefs
PATCH  /v1/me                                    update region / schedule prefs
GET    /v1/search?q=                             TMDB multi-search
GET    /v1/titles/{tmdbKind}/{tmdbId}            title detail; ingest+cache on first hit
PATCH  /v1/titles/{id}/entry                     status / rating / favorite / pointer
GET    /v1/me/shelves/{shelf}                    watchlist|rotation|watched|favorites|ratings
POST   /v1/guides                                generate for a date range
GET    /v1/guides/current                        active guide + items (feeds both views)
POST   /v1/guides/{id}/regenerate                re-plan future, unpinned, unedited items
PATCH  /v1/guides/{id}/items/{itemId}            move / swap / pin
DELETE /v1/guides/{id}/items/{itemId}            remove item from guide
POST   /v1/guides/{id}/items/{itemId}/watched    mark watched → advance pointer
```

**External-API policy:** TMDB/TVMaze called server-side only, behind a client with timeouts, retry, and per-host rate limiting. Failures degrade to cached rows (stale providers ≠ 500). Free-tier limits protected by the DB cache TTLs above.

## Frontend

Next.js App Router + TypeScript + Tailwind, deployed on Firebase App Hosting (`rootDir: web`). TanStack Query for API data with the Firebase ID token attached. Pages:

- `/guide` — calendar view (default) ⇄ guide-board toggle; retro TV-guide styling on the board.
- `/search`, `/title/[kind]/[id]`, `/profile` (shelves), `/settings` (region, viewing windows). The rotation cap is fixed at 8 in v1.
- Footer attribution: TMDB + JustWatch (providers) + TVMaze, per their free-use terms.

## Repo & Infrastructure

**Monorepo `shottah/lineup` (public GitHub):**

```
lineup/
  web/     Next.js app (App Hosting)
  api/     Go service (Cloud Run)
  infra/   cloudbuild.yaml, clouddeploy.yaml, VM setup + backup scripts
  docs/    this spec, ADRs
```

**GCP (new dedicated project, id ~`lineup-app`, adjusted for global uniqueness; Firebase enabled):**

- **API pipeline:** Cloud Build trigger on `main` pushes touching `api/**` → `go test` → build image → Artifact Registry → Cloud Deploy release → promote to Cloud Run `prod` target (staging target addable later).
- **Frontend:** App Hosting backend connected to the GitHub repo; auto-builds `web/` on push to `main`.
- **Database VM:** e2-micro (us-central1, always-free tier), Debian 12 + Postgres 16, internal IP only, SSH via IAP tunnel, firewall allowing 5432 from the VPC subnet only. Cloud Run reaches it via Direct VPC egress (free). The VM has no standing external IP; for OS updates, a short-lived external IP is attached during maintenance and removed after (scripted in `infra/`), keeping standing cost at $0 and Postgres never publicly exposed.
- **Backups:** nightly cron `pg_dump` → GCS bucket (within 5 GB free tier), 14-day retention. VM setup is an idempotent script in `infra/` — the box is rebuildable from scratch + latest dump.
- **Secrets:** Secret Manager (DB password, TMDB API key — TVMaze needs none), mounted into Cloud Run.
- **CI:** GitHub Actions on PRs (free for public repos): `go vet`/`go test`, Next.js lint + build. Cloud Build is deploy-only.

## Testing

- **Guide engine (deepest coverage):** table-driven Go tests — determinism (same seed → same guide), window fitting, air-date pinning, one-episode-per-day, round-robin fairness, variety, service cohesion, regenerate-preserves-pins-and-edits, pointer advancement/auto-complete.
- **Handlers:** `httptest` against fake stores; auth middleware unit tests.
- **External clients:** TMDB/TVMaze parsers against recorded JSON fixtures.
- **Web:** typecheck + lint; render tests of both guide views from fixture data.

## Manual Setup Steps (tracked, not automated)

1. TMDB API key (free account) → Secret Manager.
2. Firebase Auth: enable Google provider; Apple provider requires Apple Developer account (can trail launch).
3. Link billing account to the new GCP project (free-tier usage still requires billing enabled).
4. Grant Cloud Build's GitHub app access to `shottah/lineup`; connect App Hosting to the repo.

## Roadmap (v2+)

1. **Social:** friends, activity feed, lists (Letterboxd-style).
2. **Google Calendar sync:** OAuth (calendar scope), one-way push of guide items with incremental sync; enables cross-platform sharing.
3. **Shared guides:** `guide_members` with roles; family/couple co-management.
4. Notifications, provider deep links ("watch now"), native mobile apps.
