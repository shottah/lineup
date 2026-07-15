/* eslint-disable @next/next/no-img-element -- plain <img> is deliberate:
   TMDB w342 posters are pre-sized for the grid; next/image would add
   remotePatterns config and an optimization hop for CDN-optimized files
   (spec 2026-07-15-web-search-title-design.md). */
import Link from "next/link";

import { posterUrl } from "@/lib/tmdb";

// The subset of a title this card renders. SearchResult satisfies it
// structurally; shelf entries map into it with year "" (which hides the
// year segment).
export type TitleCardData = {
  tmdb_id: number;
  kind: "movie" | "series";
  name: string;
  poster_path: string;
  year: string;
};

// Poster card linking to the title page. badge renders as a small pill
// over the poster (rotation's next-episode, ratings' value).
export function TitleCard({ title, badge }: { title: TitleCardData; badge?: string }) {
  const poster = posterUrl(title.poster_path, "w342");
  return (
    <Link
      href={`/title/${title.kind}/${title.tmdb_id}`}
      className="group relative block overflow-hidden rounded-xl border border-zinc-200 dark:border-zinc-800"
    >
      {badge && (
        <span className="absolute left-2 top-2 z-10 rounded-md bg-zinc-950/80 px-1.5 py-0.5 text-xs text-zinc-50">
          {badge}
        </span>
      )}
      {poster ? (
        <img
          src={poster}
          alt={title.name}
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
          {title.name}
        </p>
        <p className="mt-0.5 text-xs text-zinc-500">
          {title.year ? `${title.year} · ${title.kind}` : title.kind}
        </p>
      </div>
    </Link>
  );
}
