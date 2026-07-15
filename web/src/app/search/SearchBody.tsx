"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { TitleCard } from "@/components/TitleCard";
import { api } from "@/lib/api";
import type { SearchResponse } from "@/lib/types";

const DEBOUNCE_MS = 300;

export function SearchBody() {
  const [input, setInput] = useState("");
  const [q, setQ] = useState("");

  // Debounce: q trails input by 300ms, so the query (keyed on q) only
  // fires when typing pauses.
  useEffect(() => {
    const t = setTimeout(() => setQ(input.trim()), DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [input]);

  const { data, error, isPending } = useQuery({
    queryKey: ["search", q],
    queryFn: () => api<SearchResponse>(`/v1/search?q=${encodeURIComponent(q)}`),
    enabled: q !== "",
  });

  return (
    <main className="mx-auto max-w-5xl p-6">
      <input
        autoFocus
        type="search"
        aria-label="Search movies and series"
        value={input}
        onChange={(e) => setInput(e.target.value)}
        placeholder="Search movies and series…"
        className="w-full rounded-lg border border-zinc-300 bg-transparent px-4 py-2 text-zinc-950 placeholder:text-zinc-400 focus:outline-none focus:ring-2 focus:ring-zinc-400 dark:border-zinc-700 dark:text-zinc-50"
      />
      {q === "" ? (
        <p className="mt-8 text-sm text-zinc-500">Search for something to watch.</p>
      ) : isPending ? (
        <p className="mt-8 text-sm text-zinc-500">Searching…</p>
      ) : error ? (
        <p className="mt-8 text-sm text-red-600">Search is unavailable right now.</p>
      ) : !data || data.results.length === 0 ? (
        <p className="mt-8 text-sm text-zinc-500">Nothing found for “{q}”.</p>
      ) : (
        <div className="mt-6 grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5">
          {data.results.map((r) => (
            <TitleCard key={`${r.kind}-${r.tmdb_id}`} result={r} />
          ))}
        </div>
      )}
    </main>
  );
}
