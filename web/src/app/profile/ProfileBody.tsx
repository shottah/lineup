"use client";

import { useState } from "react";
import Link from "next/link";
import { useQuery } from "@tanstack/react-query";

import { TitleCard, type TitleCardData } from "@/components/TitleCard";
import { api } from "@/lib/api";
import type { Entry, ShelfName, ShelfResponse } from "@/lib/types";

const TABS: { shelf: ShelfName; label: string }[] = [
  { shelf: "watchlist", label: "Watchlist" },
  { shelf: "rotation", label: "In Rotation" },
  { shelf: "watched", label: "Watched" },
  { shelf: "favorites", label: "Favorites" },
  { shelf: "ratings", label: "Ratings" },
];

const ROTATION_CAP = 8;

const EMPTY_COPY: Record<Exclude<ShelfName, "watchlist">, string> = {
  rotation: "Promote watchlist titles to build your weekly lineup.",
  watched: "Nothing marked watched yet.",
  favorites: "No favorites yet — tap the heart on a title.",
  ratings: "No ratings yet.",
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
// (the pointer the guide advances), ratings shows the value.
function badgeFor(shelf: ShelfName, e: Entry): string | undefined {
  if (shelf === "rotation" && e.kind === "series") {
    return `Next: S${e.pointer.season}E${e.pointer.episode}`;
  }
  if (shelf === "ratings" && e.rating != null) {
    return `${e.rating.toFixed(1)}★`;
  }
  return undefined;
}

function ShelfGrid({ shelf }: { shelf: ShelfName }) {
  const { data, error, isPending } = useQuery({
    queryKey: ["shelf", shelf],
    queryFn: () => api<ShelfResponse>(`/v1/me/shelves/${shelf}`),
  });

  if (isPending) {
    return <p className="mt-8 text-sm text-zinc-500">Loading…</p>;
  }
  if (error || !data) {
    return <p className="mt-8 text-sm text-red-600">Couldn’t load this shelf.</p>;
  }

  return (
    <>
      {shelf === "rotation" && (
        <p className="mt-4 text-sm text-zinc-500">
          {data.entries.length} of {ROTATION_CAP} rotation slots used
        </p>
      )}
      {data.entries.length === 0 ? (
        <p className="mt-8 text-sm text-zinc-500">
          {shelf === "watchlist" ? (
            <>
              Nothing on your watchlist yet —{" "}
              <Link href="/search" className="underline underline-offset-4">
                find something in Search
              </Link>
              .
            </>
          ) : (
            EMPTY_COPY[shelf]
          )}
        </p>
      ) : (
        <div className="mt-6 grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5">
          {data.entries.map((e) => (
            <TitleCard key={e.title_id} title={cardData(e)} badge={badgeFor(shelf, e)} />
          ))}
        </div>
      )}
    </>
  );
}

export function ProfileBody() {
  const [active, setActive] = useState<ShelfName>("watchlist");
  return (
    <main className="mx-auto max-w-5xl p-6">
      <div className="flex flex-wrap items-center gap-2">
        {TABS.map((t) => (
          <button
            key={t.shelf}
            type="button"
            aria-pressed={active === t.shelf}
            onClick={() => setActive(t.shelf)}
            className={`rounded-lg border px-3 py-1.5 text-sm ${
              active === t.shelf
                ? "border-zinc-950 bg-zinc-950 text-zinc-50 dark:border-zinc-50 dark:bg-zinc-50 dark:text-zinc-950"
                : "border-zinc-300 text-zinc-950 dark:border-zinc-700 dark:text-zinc-50"
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>
      <ShelfGrid shelf={active} />
    </main>
  );
}
