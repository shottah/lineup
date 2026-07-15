"use client";

import { useState, type ReactNode } from "react";
import Link from "next/link";
import { useQuery, type UseQueryResult } from "@tanstack/react-query";

import { TitleCard, type TitleCardData } from "@/components/TitleCard";
import { api } from "@/lib/api";
import type { Entry, ShelfName, ShelfResponse } from "@/lib/types";
import { WatchlistQuickActions } from "./WatchlistQuickActions";

const TABS: { shelf: ShelfName; label: string }[] = [
  { shelf: "watchlist", label: "Watchlist" },
  { shelf: "rotation", label: "In Rotation" },
  { shelf: "watched", label: "Watched" },
  { shelf: "favorites", label: "Favorites" },
  { shelf: "ratings", label: "Ratings" },
];

const ROTATION_CAP = 10;

const EMPTY_COPY: Record<ShelfName, ReactNode> = {
  watchlist: (
    <>
      Nothing on your watchlist yet —{" "}
      <Link href="/search" className="underline underline-offset-4">
        find something in Search
      </Link>
      .
    </>
  ),
  rotation: "Your rotation is empty — promote titles from your watchlist.",
  watched: "Nothing marked watched yet.",
  favorites: "Nothing favorited yet — tap the heart on any title.",
  ratings: "No ratings yet — rate titles from their page.",
};

function cardData(e: Entry): TitleCardData {
  return {
    tmdb_id: e.tmdb_id,
    kind: e.kind,
    name: e.name,
    poster_path: e.poster_path,
    year: "",
  };
}

// Tab-specific poster badges: rotation shows the next episode for series
// (the pointer the guide advances), ratings shows the value — star first.
function badgeFor(shelf: ShelfName, e: Entry): string | undefined {
  if (shelf === "rotation" && e.kind === "series") {
    return `Next: S${e.pointer.season}E${e.pointer.episode}`;
  }
  if (shelf === "ratings" && e.rating != null) {
    return `★ ${e.rating.toFixed(1)}`;
  }
  return undefined;
}

// All five shelves mount on load (not just the active tab) so tab pills
// can show live counts — payloads are small and invalidation already
// refetches all of them together.
function useShelf(name: ShelfName): UseQueryResult<ShelfResponse> {
  return useQuery({
    queryKey: ["shelf", name],
    queryFn: () => api<ShelfResponse>(`/v1/me/shelves/${name}`),
  });
}

function RotationMeter({ count }: { count: number }) {
  return (
    <div className="flex items-center gap-3.5 self-start rounded-[14px] border border-line bg-panel px-[18px] py-3.5">
      <div className="flex gap-[5px]">
        {Array.from({ length: ROTATION_CAP }).map((_, i) => (
          <div
            key={i}
            className={`h-[15px] w-[22px] rounded-[4px] border ${
              i < count ? "border-acc bg-acc-soft" : "border-line"
            }`}
          />
        ))}
      </div>
      <p className="text-[13px] font-medium text-mut">
        {count} of {ROTATION_CAP} rotation slots used
      </p>
    </div>
  );
}

function ShelfContent({
  shelf,
  query,
}: {
  shelf: ShelfName;
  query: UseQueryResult<ShelfResponse>;
}) {
  const { data, error, isPending } = query;

  if (isPending) {
    return <p className="text-[13.5px] text-faint">Loading…</p>;
  }
  if (error || !data) {
    return <p className="text-[13.5px] text-danger">Couldn’t load this shelf.</p>;
  }
  if (data.entries.length === 0) {
    return (
      <p className="max-w-[480px] rounded-[14px] border border-dashed border-line p-9 text-center text-[13.5px] text-faint">
        {EMPTY_COPY[shelf]}
      </p>
    );
  }
  return (
    <div className="grid gap-[18px] [grid-template-columns:repeat(auto-fill,minmax(150px,1fr))]">
      {data.entries.map((e) =>
        shelf === "watchlist" ? (
          <div key={e.title_id} className="group relative">
            <TitleCard title={cardData(e)} badge={badgeFor(shelf, e)} captionless />
            <WatchlistQuickActions entry={e} />
          </div>
        ) : (
          <TitleCard key={e.title_id} title={cardData(e)} badge={badgeFor(shelf, e)} captionless />
        ),
      )}
    </div>
  );
}

export function ProfileBody() {
  const [active, setActive] = useState<ShelfName>("watchlist");

  const watchlist = useShelf("watchlist");
  const rotation = useShelf("rotation");
  const watched = useShelf("watched");
  const favorites = useShelf("favorites");
  const ratings = useShelf("ratings");

  const queries: Record<ShelfName, UseQueryResult<ShelfResponse>> = {
    watchlist,
    rotation,
    watched,
    favorites,
    ratings,
  };

  return (
    <main className="mx-auto max-w-[1280px] px-8">
      <div className="flex flex-col gap-5 pt-[26px] pb-8">
        <h1 className="text-[22px] font-semibold tracking-tight text-ink">Your shelves</h1>
        <div className="flex flex-wrap gap-1">
          {TABS.map((t) => {
            const isActive = active === t.shelf;
            const count = queries[t.shelf].data?.entries.length;
            return (
              <button
                key={t.shelf}
                type="button"
                aria-pressed={isActive}
                onClick={() => setActive(t.shelf)}
                className={`whitespace-nowrap rounded-full px-[15px] py-[7px] text-[12.5px] font-semibold ${
                  isActive ? "bg-ink text-bg" : "bg-panel2 text-mut"
                }`}
              >
                {t.label}
                {count !== undefined && <span className="font-normal opacity-65"> {count}</span>}
              </button>
            );
          })}
        </div>
        {active === "rotation" && rotation.data && (
          <RotationMeter count={rotation.data.entries.length} />
        )}
        <ShelfContent shelf={active} query={queries[active]} />
      </div>
    </main>
  );
}
