"use client";

import { useEffect, useRef } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/lib/api";
import { browserTimezone, timezoneToSync } from "@/lib/today";
import type { User } from "@/lib/types";

// Keeps users.timezone equal to the browser's zone so the API's "today"
// (current guide, regenerate's past/future split, move rules) matches the
// local date the guide UI labels as Tonight. Runs once per signed-in app
// load; renders nothing. Shares the ["me"] query with Settings.
export function TimezoneSync() {
  const queryClient = useQueryClient();
  const { data: me } = useQuery({ queryKey: ["me"], queryFn: () => api<User>("/v1/me") });
  const sent = useRef(false);

  useEffect(() => {
    if (!me || sent.current) return;
    const tz = timezoneToSync(me.timezone, browserTimezone());
    if (tz === null) return;
    sent.current = true;
    api<User>("/v1/me", { method: "PATCH", body: JSON.stringify({ timezone: tz }) })
      .then((updated) => {
        queryClient.setQueryData(["me"], updated);
        // The guide was fetched with the old zone's "today"; refetch it.
        queryClient.invalidateQueries({ queryKey: ["guide"] });
      })
      .catch(() => {
        // Best effort: the next app load retries.
      });
  }, [me, queryClient]);

  return null;
}
