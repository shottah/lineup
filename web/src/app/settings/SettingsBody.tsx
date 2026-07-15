"use client";

import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { useToast } from "@/components/Providers";
import { api } from "@/lib/api";
import type { DayWindow, SchedulePrefs, User } from "@/lib/types";

const REGION_NAMES: Record<string, string> = {
  US: "United States",
  GB: "United Kingdom",
  CA: "Canada",
  AU: "Australia",
  DE: "Germany",
  FR: "France",
  ES: "Spain",
  IT: "Italy",
  NL: "Netherlands",
  SE: "Sweden",
  BR: "Brazil",
  MX: "Mexico",
  JP: "Japan",
  KR: "South Korea",
  IN: "India",
};

const REGIONS = Object.keys(REGION_NAMES);

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

// Auto-save debounce (approved delta): fire the PATCH this long after the
// form settles, not on every keystroke.
const AUTOSAVE_MS = 600;

type FormState = { region: string; prefs: SchedulePrefs };
type SavePayload = { region: string; schedule_prefs: SchedulePrefs };

// Seed every day defensively: rows the stored document lacks (legacy
// pre-default shapes) get the canonical default window.
function initialForm(user: User): FormState {
  return {
    region: user.region,
    prefs: {
      windows: Object.fromEntries(
        DAYS.map((d) => [d.key, user.schedule_prefs?.windows?.[d.key] ?? { ...DEFAULT_WINDOW }]),
      ),
    },
  };
}

function SettingsForm({ user }: { user: User }) {
  const queryClient = useQueryClient();
  const { show } = useToast();

  const [form, setForm] = useState<FormState>(() => initialForm(user));
  // Snapshot of the last attempted payload. Seeded from the initial load;
  // updated on both success and failure so a failed save holds until the user
  // edits again (matching explicit-save UX: "Couldn't save — try again" means
  // the user must change something to retry, not autosave retry forever).
  const lastAttempted = useRef<FormState>(form);

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
    mutationFn: (payload: SavePayload) =>
      api<User>("/v1/me", {
        method: "PATCH",
        body: JSON.stringify(payload),
      }),
    onSuccess: (_data, payload) => {
      lastAttempted.current = { region: payload.region, prefs: payload.schedule_prefs };
      show("Settings saved");
      queryClient.invalidateQueries({ queryKey: ["me"] });
    },
    onError: (_err, payload) => {
      lastAttempted.current = { region: payload.region, prefs: payload.schedule_prefs };
      show("Couldn't save — try again.");
    },
  });

  // Auto-save (approved delta, replaces the Save button): whenever the
  // form differs from the last-attempted snapshot and every row validates,
  // debounce 600ms then PATCH the full document. Skips while a save is
  // already in flight; once that save settles (isPending flips back to
  // false) the effect re-runs and re-queues if the form moved on since.
  useEffect(() => {
    if (mutation.isPending) return;
    if (invalidDays.length > 0) return;
    if (JSON.stringify(form) === JSON.stringify(lastAttempted.current)) return;

    const timer = setTimeout(() => {
      mutation.mutate({ region: form.region, schedule_prefs: form.prefs });
    }, AUTOSAVE_MS);
    return () => clearTimeout(timer);
    // Depending on the whole `mutation` object (a fresh reference every
    // render) would reset the debounce timer on unrelated re-renders; the
    // isPending and mutate members read above are what actually needs to
    // be current.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [form, invalidDays.length, mutation.isPending, mutation.mutate]);

  // Latest form/validity, kept current every render via an effect (refs
  // can't be written during render itself) so the unmount-only flush below
  // can read the value as of the last render without depending on it and
  // re-running.
  const latestRef = useRef<{ form: FormState; valid: boolean }>({
    form,
    valid: invalidDays.length === 0,
  });
  useEffect(() => {
    latestRef.current = { form, valid: invalidDays.length === 0 };
  });

  const mutateRef = useRef(mutation.mutate);
  useEffect(() => {
    mutateRef.current = mutation.mutate;
  });

  // Data-loss fix: a debounced save queued by the effect above is dropped
  // if the component unmounts (e.g. the user navigates away) before the
  // 600ms timer fires. On unmount only, flush the latest valid form if it
  // hasn't already been attempted. TanStack Query v5 mutations outlive the
  // unmounted component, so this PATCH still completes even though there's
  // no one left to show the success toast.
  useEffect(() => {
    return () => {
      const { form: latestForm, valid } = latestRef.current;
      if (valid && JSON.stringify(latestForm) !== JSON.stringify(lastAttempted.current)) {
        mutateRef.current({ region: latestForm.region, schedule_prefs: latestForm.prefs });
      }
    };
    // Empty deps are intentional: this must run its cleanup only once, on
    // unmount. Reading through latestRef/mutateRef (kept current by the
    // no-dependency effects above, which run after every render) avoids
    // re-subscribing this effect on every form change.
  }, []);

  return (
    <>
      <div className="flex items-center justify-between gap-4 rounded-[14px] border border-line bg-panel px-5 py-4">
        <div>
          <div className="text-[14px] font-semibold text-ink">Region</div>
          <div className="mt-0.5 text-[12px] text-mut">Sets where-to-watch availability</div>
        </div>
        <select
          value={form.region}
          onChange={(e) => setForm((f) => ({ ...f, region: e.target.value }))}
          aria-label="Region"
          className="rounded-[10px] border border-line bg-panel2 px-3 py-2 text-[13px] font-medium text-ink"
        >
          {regions.map((code) => (
            <option key={code} value={code}>
              {REGION_NAMES[code] ?? code}
            </option>
          ))}
        </select>
      </div>

      <div>
        <h2 className="mb-1 text-[14px] font-semibold text-ink">Viewing window</h2>
        <p className="mb-3 text-[12.5px] text-mut">
          {"When you're free to watch each night — your guide only schedules inside these hours."}
        </p>
        <div className="flex flex-col gap-1.5">
          {DAYS.map((d) => {
            const w = form.prefs.windows[d.key];
            const invalid = invalidDays.includes(d.key);
            return (
              <div key={d.key} className="rounded-xl border border-line bg-panel px-4 py-2.5">
                <div className="flex items-center gap-3.5">
                  <button
                    type="button"
                    role="switch"
                    aria-checked={w.enabled}
                    aria-label={d.label}
                    onClick={() => setWindow(d.key, { enabled: !w.enabled })}
                    className={`relative h-[22px] w-[38px] flex-none rounded-full transition-colors duration-150 ${
                      w.enabled ? "bg-acc" : "bg-panel2"
                    }`}
                  >
                    <span
                      className={`absolute top-[3px] left-[3px] h-4 w-4 rounded-full bg-white transition-transform duration-150 ${
                        w.enabled ? "translate-x-4" : "translate-x-0"
                      }`}
                    />
                  </button>
                  <div
                    className={`w-[86px] text-[13px] font-semibold ${
                      w.enabled ? "text-ink" : "text-faint"
                    }`}
                  >
                    {d.label}
                  </div>
                  <input
                    type="time"
                    value={w.start}
                    disabled={!w.enabled}
                    aria-label={`${d.label} start`}
                    onChange={(e) => setWindow(d.key, { start: e.target.value })}
                    className="rounded-lg border border-line bg-panel2 px-2 py-[5px] text-[12.5px] font-medium text-ink disabled:opacity-50"
                  />
                  <span className="text-[12px] text-faint">to</span>
                  <input
                    type="time"
                    value={w.end}
                    disabled={!w.enabled}
                    aria-label={`${d.label} end`}
                    onChange={(e) => setWindow(d.key, { end: e.target.value })}
                    className="rounded-lg border border-line bg-panel2 px-2 py-[5px] text-[12.5px] font-medium text-ink disabled:opacity-50"
                  />
                </div>
                {invalid && (
                  <p className="mt-1.5 ml-[52px] text-[11.5px] font-medium text-danger">
                    End must be after start.
                  </p>
                )}
              </div>
            );
          })}
        </div>
      </div>
    </>
  );
}

export function SettingsBody() {
  const { data, error, isPending } = useQuery({
    queryKey: ["me"],
    queryFn: () => api<User>("/v1/me"),
  });

  if (isPending) {
    return <p className="p-8 text-sm text-mut">Loading…</p>;
  }
  if (error || !data) {
    return <p className="p-8 text-sm text-danger">Could not load your profile.</p>;
  }

  return (
    <main className="mx-auto max-w-[1280px] px-8">
      <div className="flex max-w-[620px] flex-col gap-6 pt-[26px] pb-8">
        <h1 className="text-[22px] font-semibold tracking-tight text-ink">Settings</h1>
        <SettingsForm user={data} />
      </div>
    </main>
  );
}
