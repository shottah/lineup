/* eslint-disable @next/next/no-img-element -- plain <img> is deliberate:
   TMDB w342 posters are pre-sized for the grid; next/image would add
   remotePatterns config and an optimization hop for CDN-optimized files
   (spec 2026-07-15-web-search-title-design.md). */
import Link from "next/link";

import { posterUrl } from "@/lib/tmdb";
import type { SearchResult } from "@/lib/types";

// Poster card linking to the title page.
export function TitleCard({ result }: { result: SearchResult }) {
  const poster = posterUrl(result.poster_path, "w342");
  return (
    <Link
      href={`/title/${result.kind}/${result.tmdb_id}`}
      className="group block overflow-hidden rounded-xl border border-zinc-200 dark:border-zinc-800"
    >
      {poster ? (
        <img
          src={poster}
          alt={result.name}
          loading="lazy"
          className="aspect-[2/3] w-full object-cover"
        />
      ) : (
        <div className="flex aspect-[2/3] w-full items-center justify-center bg-zinc-100 text-xs text-zinc-400 dark:bg-zinc-900">
          no poster
        </div>
      )}
      <div className="p-3">
        <p className="truncate text-sm font-medium text-zinc-950 group-hover:underline dark:text-zinc-50">
          {result.name}
        </p>
        <p className="mt-0.5 text-xs text-zinc-500">
          {result.year || "—"} · {result.kind}
        </p>
      </div>
    </Link>
  );
}
