"use client";

import { useEffect, useState } from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";

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
    // Keep the previous grid rendered while the next debounced query
    // loads, and cap retries like the title query (default is 3 with
    // exponential backoff — ~15s of "Searching…" when the API is down).
    placeholderData: keepPreviousData,
    retry: 2,
  });

  return (
    <main className="mx-auto max-w-[1280px] px-8">
      <div className="flex flex-col gap-[22px] pt-[26px] pb-8">
        <input
          autoFocus
          type="search"
          aria-label="Search movies and series"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder="Search movies and series…"
          className="w-full max-w-[560px] rounded-xl border border-line bg-panel px-[18px] py-[13px] text-[15px] text-ink placeholder:text-faint"
        />
        {q === "" ? (
          <p className="text-[13.5px] text-faint">
            Type to search — results appear as you type.
          </p>
        ) : isPending ? (
          <p className="text-[13.5px] text-faint">Searching…</p>
        ) : error ? (
          <p className="text-sm text-danger">Search is unavailable right now.</p>
        ) : !data || data.results.length === 0 ? (
          <p className="text-[13.5px] text-faint">No matches. Try a different spelling.</p>
        ) : (
          <div className="grid gap-[18px] [grid-template-columns:repeat(auto-fill,minmax(150px,1fr))]">
            {data.results.map((r) => (
              <TitleCard key={`${r.kind}-${r.tmdb_id}`} title={r} />
            ))}
          </div>
        )}
      </div>
    </main>
  );
}
