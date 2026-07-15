"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { useToast } from "@/components/Providers";
import { api } from "@/lib/api";
import type { DayWindow, SchedulePrefs, User } from "@/lib/types";

const REGIONS = [
  "US", "GB", "CA", "AU", "DE", "FR", "ES", "IT", "NL", "SE", "BR", "MX", "JP", "KR", "IN",
];

const DAYS: { key: string; label: string }[] = [
  { key: "mon", label: "Mon" },
  { key: "tue", label: "Tue" },
  { key: "wed", label: "Wed" },
  { key: "thu", label: "Thu" },
  { key: "fri", label: "Fri" },
  { key: "sat", label: "Sat" },
  { key: "sun", label: "Sun" },
];

const DEFAULT_WINDOW: DayWindow = { enabled: true, start: "19:00", end: "23:00" };

type FormState = { region: string; prefs: SchedulePrefs };

function SettingsForm({ user }: { user: User }) {
  const queryClient = useQueryClient();
  const { show } = useToast();

  // Seed every day defensively: rows the stored document lacks (legacy
  // pre-default shapes) get the canonical default window.
  const [form, setForm] = useState<FormState>(() => ({
    region: user.region,
    prefs: {
      windows: Object.fromEntries(
        DAYS.map((d) => [d.key, user.schedule_prefs?.windows?.[d.key] ?? { ...DEFAULT_WINDOW }]),
      ),
    },
  }));

  const regions = REGIONS.includes(form.region) ? REGIONS : [form.region, ...REGIONS];

  const setWindow = (day: string, patch: Partial<DayWindow>) =>
    setForm((f) => ({
      ...f,
      prefs: {
        windows: { ...f.prefs.windows, [day]: { ...f.prefs.windows[day], ...patch } },
      },
    }));

  // Server rule (prefs.Validate): start < end on EVERY row, enabled or
  // not. Zero-padded HH:MM compares correctly as strings.
  const invalidDays = DAYS.filter((d) => {
    const w = form.prefs.windows[d.key];
    // A cleared native time input reports "" — invalid, like start >= end.
    return !w.start || !w.end || w.start >= w.end;
  }).map((d) => d.key);

  const mutation = useMutation({
    mutationFn: () =>
      api<User>("/v1/me", {
        method: "PATCH",
        body: JSON.stringify({ region: form.region, schedule_prefs: form.prefs }),
      }),
    onSuccess: () => {
      show("Settings saved");
      queryClient.invalidateQueries({ queryKey: ["me"] });
    },
    onError: () => show("Couldn't save — try again."),
  });

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        mutation.mutate();
      }}
      className="mt-6 flex max-w-xl flex-col gap-6"
    >
      <label className="flex items-center gap-3 text-sm text-zinc-950 dark:text-zinc-50">
        Region
        <select
          value={form.region}
          onChange={(e) => setForm((f) => ({ ...f, region: e.target.value }))}
          className="rounded-lg border border-zinc-300 bg-transparent px-2 py-1 dark:border-zinc-700"
        >
          {regions.map((r) => (
            <option key={r} value={r}>
              {r}
            </option>
          ))}
        </select>
      </label>

      <fieldset className="flex flex-col gap-2">
        <legend className="text-sm font-medium text-zinc-950 dark:text-zinc-50">
          Viewing windows
        </legend>
        {DAYS.map((d) => {
          const w = form.prefs.windows[d.key];
          const invalid = invalidDays.includes(d.key);
          return (
            <div key={d.key} className="flex items-center gap-3 text-sm">
              <label className="flex w-24 items-center gap-2 text-zinc-950 dark:text-zinc-50">
                <input
                  type="checkbox"
                  checked={w.enabled}
                  onChange={(e) => setWindow(d.key, { enabled: e.target.checked })}
                />
                {d.label}
              </label>
              <input
                type="time"
                value={w.start}
                disabled={!w.enabled}
                aria-label={`${d.label} start`}
                onChange={(e) => setWindow(d.key, { start: e.target.value })}
                className="rounded-lg border border-zinc-300 bg-transparent px-2 py-1 disabled:opacity-50 dark:border-zinc-700"
              />
              <span className="text-zinc-500">to</span>
              <input
                type="time"
                value={w.end}
                disabled={!w.enabled}
                aria-label={`${d.label} end`}
                onChange={(e) => setWindow(d.key, { end: e.target.value })}
                className="rounded-lg border border-zinc-300 bg-transparent px-2 py-1 disabled:opacity-50 dark:border-zinc-700"
              />
              {invalid && <span className="text-xs text-red-600">start must be before end</span>}
            </div>
          );
        })}
      </fieldset>

      <button
        type="submit"
        disabled={mutation.isPending || invalidDays.length > 0}
        className="w-fit rounded-lg bg-zinc-950 px-4 py-2 text-sm text-zinc-50 disabled:opacity-50 dark:bg-zinc-50 dark:text-zinc-950"
      >
        Save settings
      </button>
    </form>
  );
}

export function SettingsBody() {
  const { data, error, isPending } = useQuery({
    queryKey: ["me"],
    queryFn: () => api<User>("/v1/me"),
  });

  if (isPending) {
    return <p className="p-8 text-sm text-zinc-500">Loading…</p>;
  }
  if (error || !data) {
    return <p className="p-8 text-sm text-red-600">Could not load your profile.</p>;
  }

  return (
    <main className="mx-auto max-w-5xl p-6">
      <h1 className="text-xl font-semibold text-zinc-950 dark:text-zinc-50">Settings</h1>
      <SettingsForm user={data} />
    </main>
  );
}
