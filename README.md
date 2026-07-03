# Lineup

Your week of TV, planned like a lineup.

Lineup brings back TV-guide-style viewing: build a profile of shows and movies (watchlist, watched, ratings, favorites), promote titles into your active rotation, and generate a personal TV guide for the week — viewable as a calendar or a classic guide board organized by streaming service.

- **Design spec:** [docs/superpowers/specs/2026-07-03-lineup-design.md](docs/superpowers/specs/2026-07-03-lineup-design.md)
- **Stack:** Next.js (Firebase App Hosting) · Go REST API (Cloud Run via Cloud Build + Cloud Deploy) · Postgres · Firebase Auth
- **Data:** [TMDB](https://www.themoviedb.org/) (metadata & watch providers, powered by JustWatch) and [TVMaze](https://www.tvmaze.com/) (episode air dates)

This product uses the TMDB API but is not endorsed or certified by TMDB.
