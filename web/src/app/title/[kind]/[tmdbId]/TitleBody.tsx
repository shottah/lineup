/* eslint-disable @next/next/no-img-element -- plain <img> is deliberate:
   TMDB files are pre-sized; next/image would add remotePatterns config
   and an optimization hop (spec 2026-07-15-web-search-title-design.md). */
"use client";

import { useRouter } from "next/navigation";
import { useQuery } from "@tanstack/react-query";

import { EntryActions } from "@/components/EntryActions";
import { api, ApiError } from "@/lib/api";
import { posterUrl } from "@/lib/tmdb";
import type { TitleFull } from "@/lib/types";

// No-poster mark: first letters of up to three words, uppercase. Mirrors
// TitleCard's fallback so the two poster slots read as the same system.
function initials(name: string): string {
  return name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 3)
    .map((word) => word[0]?.toUpperCase() ?? "")
    .join("");
}

export function TitleBody({ kind, tmdbId }: { kind: string; tmdbId: string }) {
  const router = useRouter();
  const { data, error, isPending } = useQuery({
    queryKey: ["title", kind, tmdbId],
    queryFn: () => api<TitleFull>(`/v1/titles/${kind}/${tmdbId}`),
    // 404 is a definitive answer; retrying it just delays the empty state.
    retry: (failureCount, err) =>
      !(err instanceof ApiError && err.status === 404) && failureCount < 2,
  });

  if (isPending) {
    return <p className="p-8 text-sm text-mut">Loading…</p>;
  }
  // Error states only when there is nothing to show: a background refetch
  // failure (e.g. after a mutation invalidates this query) keeps rendering
  // the cached page rather than collapsing it mid-interaction.
  if (!data) {
    const notFound = error instanceof ApiError && error.status === 404;
    return (
      <p className="p-8 text-sm text-mut">
        {notFound ? "Title not found." : "Can't reach the catalog right now — try again shortly."}
      </p>
    );
  }

  const { title, seasons, providers, entry } = data;
  const poster = posterUrl(title.poster_path, "w342");

  return (
    <main className="mx-auto max-w-[1280px] px-8">
      <div className="pt-6 pb-8">
        <button
          type="button"
          onClick={() => {
            // Deep links have no in-app history to pop; fall back to search.
            if (window.history.length > 1) {
              router.back();
            } else {
              router.push("/search");
            }
          }}
          className="pb-5 text-[13px] font-medium text-mut"
        >
          ← Back
        </button>
        <div className="flex flex-wrap gap-9">
          {poster ? (
            <img
              src={poster}
              alt={title.name}
              className="h-fit w-[220px] shrink-0 rounded-[14px]"
            />
          ) : (
            <div className="flex aspect-[2/3] w-[220px] shrink-0 flex-col items-center justify-center gap-1.5 rounded-[14px] border border-dashed border-line bg-panel2">
              <span className="text-xl font-semibold text-faint">{initials(title.name)}</span>
              <span className="text-[10.5px] text-faint">No poster</span>
            </div>
          )}
          <div className="flex min-w-[320px] max-w-[640px] flex-1 flex-col gap-3.5">
            <div>
              <h1 className="text-[32px] font-semibold leading-[1.1] tracking-tight text-ink">
                {title.name}
              </h1>
              <p className="mt-1.5 text-[13.5px] text-mut">
                {title.kind === "movie"
                  ? title.runtime_minutes > 0
                    ? `Movie · ${title.runtime_minutes} min`
                    : "Movie"
                  : `Series${
                      seasons.length > 0
                        ? ` · ${seasons.length} season${seasons.length === 1 ? "" : "s"}`
                        : ""
                    }${title.airing ? " · airing" : ""}`}
              </p>
            </div>
            {title.overview && (
              <p className="text-[14.5px] leading-relaxed text-mut">{title.overview}</p>
            )}

            <div>
              <h2 className="mb-2 text-[10.5px] font-semibold tracking-[.14em] text-faint">
                WHERE TO WATCH
              </h2>
              {providers.length === 0 ? (
                <p className="text-[13px] text-faint">Not streaming in your region</p>
              ) : (
                <div className="flex flex-wrap gap-2">
                  {providers.map((p) => {
                    const logo = posterUrl(p.logo_path, "w92");
                    return (
                      <span
                        key={p.id}
                        className="flex items-center gap-2 rounded-full border border-line bg-panel py-1.5 pl-[7px] pr-3.5"
                      >
                        {logo ? (
                          <img
                            src={logo}
                            alt=""
                            className="h-[22px] w-[22px] rounded-md object-cover"
                          />
                        ) : (
                          <span className="flex h-[22px] w-[22px] items-center justify-center rounded-md bg-acc-soft text-[11px] font-bold text-acc">
                            {p.name[0]?.toUpperCase() ?? ""}
                          </span>
                        )}
                        <span className="text-[12.5px] font-medium text-ink">{p.name}</span>
                        <span className="text-[11px] text-faint">Stream</span>
                      </span>
                    );
                  })}
                </div>
              )}
            </div>

            <EntryActions title={title} entry={entry} kind={kind} tmdbId={tmdbId} />
          </div>
        </div>
      </div>
    </main>
  );
}
