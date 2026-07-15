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

const KIND_LABEL: Record<TitleCardData["kind"], string> = {
  movie: "Movie",
  series: "Series",
};

// No-poster mark: first letters of up to three words, uppercase.
function initials(name: string): string {
  return name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 3)
    .map((word) => word[0]?.toUpperCase() ?? "")
    .join("");
}

// Poster card linking to the title page. badge renders as a small pill
// over the poster (rotation's next-episode, ratings' value). captionless
// suppresses the "No poster" caption under the no-poster fallback — the
// search grid shows it, the profile shelf grid (tighter cards) doesn't.
export function TitleCard({
  title,
  badge,
  captionless = false,
}: {
  title: TitleCardData;
  badge?: string;
  captionless?: boolean;
}) {
  const poster = posterUrl(title.poster_path, "w342");
  const kindLabel = KIND_LABEL[title.kind];
  const meta = title.year ? `${title.year} · ${kindLabel}` : kindLabel;

  return (
    <Link href={`/title/${title.kind}/${title.tmdb_id}`} className="group relative block text-left">
      {badge && (
        <span className="absolute left-2 top-2 z-10 rounded-full bg-acc px-[9px] py-[3px] text-[10px] font-semibold text-acc-ink">
          {badge}
        </span>
      )}
      {poster ? (
        <img
          src={poster}
          alt={title.name}
          loading="lazy"
          className="aspect-[2/3] w-full rounded-xl object-cover"
        />
      ) : (
        <div className="flex aspect-[2/3] w-full flex-col items-center justify-center gap-1.5 rounded-xl border border-dashed border-line bg-panel2">
          <span className="text-xl font-semibold text-faint">{initials(title.name)}</span>
          {!captionless && <span className="text-[10.5px] text-faint">No poster</span>}
        </div>
      )}
      <p className="mt-[9px] truncate text-[13.5px] font-semibold text-ink group-hover:underline">
        {title.name}
      </p>
      <p className="mt-0.5 text-[11.5px] text-mut">{meta}</p>
    </Link>
  );
}
