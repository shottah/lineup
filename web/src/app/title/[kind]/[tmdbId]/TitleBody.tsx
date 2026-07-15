/* eslint-disable @next/next/no-img-element -- plain <img> is deliberate:
   TMDB files are pre-sized; next/image would add remotePatterns config
   and an optimization hop (spec 2026-07-15-web-search-title-design.md). */
"use client";

import { useQuery } from "@tanstack/react-query";

import { EntryActions } from "@/components/EntryActions";
import { api, ApiError } from "@/lib/api";
import { posterUrl } from "@/lib/tmdb";
import type { TitleFull } from "@/lib/types";

export function TitleBody({ kind, tmdbId }: { kind: string; tmdbId: string }) {
  const { data, error, isPending } = useQuery({
    queryKey: ["title", kind, tmdbId],
    queryFn: () => api<TitleFull>(`/v1/titles/${kind}/${tmdbId}`),
    // 404 is a definitive answer; retrying it just delays the empty state.
    retry: (failureCount, err) =>
      !(err instanceof ApiError && err.status === 404) && failureCount < 2,
  });

  if (isPending) {
    return <p className="p-8 text-sm text-zinc-500">Loading…</p>;
  }
  if (error || !data) {
    const notFound = error instanceof ApiError && error.status === 404;
    return (
      <p className="p-8 text-sm text-zinc-500">
        {notFound ? "Title not found." : "Can't reach the catalog right now — try again shortly."}
      </p>
    );
  }

  const { title, seasons, providers, entry } = data;
  const poster = posterUrl(title.poster_path, "w342");

  return (
    <main className="mx-auto flex max-w-4xl flex-col gap-8 p-6 sm:flex-row">
      {poster ? (
        <img src={poster} alt={title.name} className="h-fit w-56 shrink-0 rounded-xl" />
      ) : (
        <div className="flex aspect-[2/3] w-56 shrink-0 items-center justify-center rounded-xl bg-zinc-100 text-xs text-zinc-400 dark:bg-zinc-900">
          no poster
        </div>
      )}
      <div className="min-w-0">
        <h1 className="text-2xl font-semibold text-zinc-950 dark:text-zinc-50">{title.name}</h1>
        <p className="mt-1 text-sm text-zinc-500">
          {title.kind === "movie"
            ? `Movie · ${title.runtime_minutes} min`
            : `Series · ${seasons.length} season${seasons.length === 1 ? "" : "s"}${
                title.airing ? " · airing" : ""
              }`}
        </p>
        {title.overview && (
          <p className="mt-4 text-sm leading-6 text-zinc-700 dark:text-zinc-300">{title.overview}</p>
        )}

        <div className="mt-6">
          <h2 className="text-sm font-medium text-zinc-950 dark:text-zinc-50">Where to watch</h2>
          {providers.length === 0 ? (
            <p className="mt-2 text-sm text-zinc-500">Not streaming in your region.</p>
          ) : (
            <div className="mt-2 flex flex-wrap items-center gap-3">
              {providers.map((p) => {
                const logo = posterUrl(p.logo_path, "w92");
                return (
                  <span
                    key={p.id}
                    className="flex items-center gap-2 rounded-lg border border-zinc-200 px-2 py-1 text-sm text-zinc-950 dark:border-zinc-800 dark:text-zinc-50"
                  >
                    {logo && <img src={logo} alt="" className="h-5 w-5 rounded" />}
                    {p.name}
                  </span>
                );
              })}
            </div>
          )}
        </div>

        <EntryActions title={title} entry={entry} kind={kind} tmdbId={tmdbId} />
      </div>
    </main>
  );
}
