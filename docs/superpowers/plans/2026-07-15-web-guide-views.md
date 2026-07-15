# Web Guide Views Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The guide calendar + board views with full editing, per issue #18 and `docs/superpowers/specs/2026-07-15-web-guide-views-design.md`.

**Architecture:** One Go commit enriches guide responses with `titles`/`providers` sidecar maps; the web renders exclusively from `GET /v1/guides/current` through pure, vitest-covered mappers in `lib/guide.ts`; every mutation invalidates `["guide"]` (watched also `["shelf"]`). Views follow the design reference's `isGuide` blocks.

**Tech Stack:** Go/chi/pgx (api), Next 16 / React 19 / TanStack Query 5 / Tailwind v4 tokens (web), vitest (NEW devDependency — mandated by the issue), pnpm.

**PLAN STYLE:** Tasks 1–2 carry complete code (transcription tier). Tasks 3–5 are prose+reference (sonnet tier): implementers read the spec and `docs/design/lineup-app.dc.html` (`isGuide` blocks — visual truth) and exercise judgment; pinned copy/tokens are exact.

## Global Constraints

- Branch `feat/18-web-guide-views`. Never touch main.
- Gates: Go tasks `gofmt -l .` silent + `go vet ./...` + `go test ./...` (store tests need `TEST_DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup?sslmode=disable'`, container `lineup-pg`); web tasks `cd web && pnpm lint && pnpm test && pnpm build` all clean.
- The ONLY package.json change in this issue: `vitest` devDependency + `"test": "vitest run"` script (Task 2).
- Query keys: guide `["guide"]`; shelves `["shelf", name]`. Mark-watched invalidates `["guide"]` AND the `["shelf"]` prefix (ledgered #17 constraint). All other guide mutations invalidate `["guide"]` on settled.
- Design tokens only (no zinc/hex); typographic characters in copy are exact (· — ✓ ↻ →).
- Pinned toasts: `Watched · {title}` / `Pinned to {DOW}` / `Unpinned` / `Swapped in {title}` / `That title can't be swapped in.` / `Moved` / `Removed — enjoy the free hour` / `Re-planned your remaining evenings — watched and pinned stayed put` / `Couldn't generate — check the dates.` / generic `Couldn't save — try again.`
- Sidecar lookups may miss an id in pathological cases: mappers fall back to `Unknown title` / empty provider name — never crash.

---

### Task 1: API — guide response sidecars

**Files:**
- Modify: `api/internal/store/guides.go` (append)
- Modify: `api/internal/httpserver/guides.go` (interface + wrapper + 3 encode sites)
- Modify: `api/internal/httpserver/guides_test.go` (fake + assertions)
- Modify: `api/internal/store/guides_test.go` (append integration test)

**Interfaces:**
- Produces: `store.TitleLookup{Name string \`json:"name"\`; Kind string \`json:"kind"\`; TMDBID int64 \`json:"tmdb_id"\`}`; `(*Store).GuideLookups(ctx, guideID int64) (map[int64]TitleLookup, map[int64]ProviderRow, error)`; guide-returning endpoints wrap as `{...Guide, "titles": {...}, "providers": {...}}`.

- [ ] **Step 1: Failing handler test** — in `api/internal/httpserver/guides_test.go`, the fake guide store gains:

```go
func (f *fakeGuides) GuideLookups(_ context.Context, _ int64) (map[int64]store.TitleLookup, map[int64]store.ProviderRow, error) {
	titles := map[int64]store.TitleLookup{}
	provs := map[int64]store.ProviderRow{}
	for _, it := range f.guide.Items {
		titles[it.TitleID] = store.TitleLookup{Name: fmt.Sprintf("Title %d", it.TitleID), Kind: "series", TMDBID: it.TitleID + 100000}
		provs[it.ProviderID] = store.ProviderRow{ID: it.ProviderID, Name: fmt.Sprintf("Provider %d", it.ProviderID), LogoPath: ""}
	}
	return titles, provs, nil
}
```

(Adapt the field/receiver names to the actual fake — read it first; if the fake stores guides differently, derive the maps from whatever guide it returns.) Then append a test:

```go
func TestCurrentGuideCarriesSidecars(t *testing.T) {
	// Build the standard guides test server with a fake guide that has at
	// least one item (reuse the file's existing helpers/fixtures).
	// GET /v1/guides/current with the standard auth token.
	// Decode into:
	var body struct {
		Items     []store.GuideItem                `json:"items"`
		Titles    map[string]store.TitleLookup     `json:"titles"`
		Providers map[string]store.ProviderRow     `json:"providers"`
	}
	// Assert: for EVERY item, body.Titles[strconv.FormatInt(it.TitleID,10)]
	// exists with non-empty Name, and body.Providers[...ProviderID...]
	// exists with non-empty Name.
}
```

(The comment lines describe assertions to write out in full — use the file's existing request/decode helpers; this is the one place you author test plumbing to match the file's local style.)

- [ ] **Step 2: RED** — `cd api && go test ./internal/httpserver/ -run TestCurrentGuideCarriesSidecars` fails: `GuideLookups` not in interface / undefined `store.TitleLookup`.

- [ ] **Step 3: Implement.** Append to `api/internal/store/guides.go`:

```go
// TitleLookup is the guide sidecar's per-title rendering data (#18).
type TitleLookup struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	TMDBID int64  `json:"tmdb_id"`
}

// GuideLookups returns rendering dictionaries for a guide's items: every
// distinct title and provider referenced, keyed by id. Ownership is the
// caller's concern (handlers resolve the guide by user first).
func (s *Store) GuideLookups(ctx context.Context, guideID int64) (map[int64]TitleLookup, map[int64]ProviderRow, error) {
	titles := map[int64]TitleLookup{}
	rows, err := s.Pool.Query(ctx, `
SELECT DISTINCT t.id, t.name, t.kind, t.tmdb_id
FROM guide_items gi JOIN titles t ON t.id = gi.title_id
WHERE gi.guide_id = $1`, guideID)
	if err != nil {
		return nil, nil, fmt.Errorf("store: guide lookups: titles: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var tl TitleLookup
		if err := rows.Scan(&id, &tl.Name, &tl.Kind, &tl.TMDBID); err != nil {
			return nil, nil, fmt.Errorf("store: guide lookups: titles scan: %w", err)
		}
		titles[id] = tl
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("store: guide lookups: titles rows: %w", err)
	}

	provs := map[int64]ProviderRow{}
	prows, err := s.Pool.Query(ctx, `
SELECT DISTINCT p.id, p.name, p.logo_path
FROM guide_items gi JOIN providers p ON p.id = gi.provider_id
WHERE gi.guide_id = $1`, guideID)
	if err != nil {
		return nil, nil, fmt.Errorf("store: guide lookups: providers: %w", err)
	}
	defer prows.Close()
	for prows.Next() {
		var p ProviderRow
		if err := prows.Scan(&p.ID, &p.Name, &p.LogoPath); err != nil {
			return nil, nil, fmt.Errorf("store: guide lookups: providers scan: %w", err)
		}
		provs[p.ID] = p
	}
	if err := prows.Err(); err != nil {
		return nil, nil, fmt.Errorf("store: guide lookups: providers rows: %w", err)
	}
	return titles, provs, nil
}
```

In `api/internal/httpserver/guides.go`: add to the `GuideStore` interface:

```go
	GuideLookups(ctx context.Context, guideID int64) (map[int64]store.TitleLookup, map[int64]store.ProviderRow, error)
```

Add the wrapper + helper:

```go
// guideResponse decorates a guide with rendering dictionaries so the web
// can resolve item ids without extra round trips (#18).
type guideResponse struct {
	*store.Guide
	Titles    map[int64]store.TitleLookup `json:"titles"`
	Providers map[int64]store.ProviderRow `json:"providers"`
}

// writeGuide responds with the guide plus its sidecars.
func writeGuide(w http.ResponseWriter, r *http.Request, d Deps, g *store.Guide) {
	titles, provs, err := d.Guides.GuideLookups(r.Context(), g.ID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(guideResponse{Guide: g, Titles: titles, Providers: provs})
}
```

Replace the three guide-encode sites (create ~:97, current ~:114, regenerate ~:200 — each currently `w.Header().Set(...)` + `json.NewEncoder(w).Encode(<guide>)`, keeping any WriteHeader(201) that precedes them) with `writeGuide(w, r, d, <guide>)`. Item-level encodes (~:275, ~:320) unchanged.

Append to `api/internal/store/guides_test.go` an integration test: create a guide via the file's existing fixtures/helpers, call `GuideLookups`, assert every item's title id and provider id resolve with non-empty names and the right tmdb_id/kind for a known seeded title.

- [ ] **Step 4: GREEN** — `cd api && gofmt -l . && go vet ./... && TEST_DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup?sslmode=disable' go test ./... && go test ./...` all green (both runs).

- [ ] **Step 5: Commit** — `feat(api): guide response sidecars for title and provider rendering`

---

### Task 2: Web — vitest infra, guide types, pure mappers

**Files:**
- Modify: `web/package.json` (vitest devDep + test script) and lockfile via `pnpm install`
- Modify: `.github/workflows/web-ci.yml` (add `- run: pnpm run test` between lint and build)
- Modify: `web/src/lib/types.ts` (append)
- Create: `web/src/lib/guide.ts`
- Create: `web/src/lib/guide.test.ts`

**Interfaces:**
- Produces (Tasks 3–5 rely on): the types below; `fmtTime`, `toCalendarColumns`, `toBoardRows` with the exact signatures/shapes below.

- [ ] **Step 1: Install vitest** — `cd web && pnpm add -D vitest` (lockfile updates); add `"test": "vitest run"` to scripts. Add the CI step.

- [ ] **Step 2: Append guide types** to `web/src/lib/types.ts`:

```ts

// --- Guide (#18). Mirrors /v1/guides JSON incl. the sidecar maps.

export type GuideItem = {
  id: number;
  date: string; // "YYYY-MM-DD"
  start_min: number; // minutes from midnight
  end_min: number;
  title_id: number;
  season: number;
  episode: number;
  provider_id: number;
  is_plan: boolean;
  pinned: boolean;
  edited: boolean;
  watched: boolean;
};

export type GuideTitleLookup = {
  name: string;
  kind: "movie" | "series";
  tmdb_id: number;
};

export type GuideResponse = {
  id: number;
  start_date: string;
  end_date: string;
  seed: number;
  items: GuideItem[];
  titles: Record<string, GuideTitleLookup>;
  providers: Record<string, ProviderRow>;
};
```

- [ ] **Step 3: Failing mapper tests** — create `web/src/lib/guide.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import type { GuideResponse } from "./types";
import { fmtTime, toBoardRows, toCalendarColumns } from "./guide";

// Two-day fixture: day 1 has a movie plan (20:00 on provider 8), a
// watched+pinned series plan (21:30 on provider 9), an alternate sharing
// the 20:00 slot (provider 9), and an alternate for the 21:30 slot
// (provider 8); day 2 is a night off. Title 5 is missing from the
// sidecar (fallback path).
const g: GuideResponse = {
  id: 1,
  start_date: "2026-07-20",
  end_date: "2026-07-21",
  seed: 42,
  items: [
    { id: 11, date: "2026-07-20", start_min: 1200, end_min: 1320, title_id: 1, season: 0, episode: 0, provider_id: 8, is_plan: true, pinned: false, edited: false, watched: false },
    { id: 12, date: "2026-07-20", start_min: 1290, end_min: 1350, title_id: 2, season: 2, episode: 5, provider_id: 9, is_plan: true, pinned: true, edited: false, watched: true },
    { id: 13, date: "2026-07-20", start_min: 1200, end_min: 1260, title_id: 3, season: 1, episode: 3, provider_id: 9, is_plan: false, pinned: false, edited: false, watched: false },
    { id: 14, date: "2026-07-20", start_min: 1290, end_min: 1350, title_id: 5, season: 4, episode: 1, provider_id: 8, is_plan: false, pinned: false, edited: false, watched: false },
  ],
  titles: {
    "1": { name: "Past Lives", kind: "movie", tmdb_id: 666277 },
    "2": { name: "Severance", kind: "series", tmdb_id: 95396 },
    "3": { name: "Slow Horses", kind: "series", tmdb_id: 95480 },
  },
  providers: {
    "8": { id: 8, name: "Netflix", logo_path: "/n.jpg" },
    "9": { id: 9, name: "Apple TV+", logo_path: "/a.jpg" },
  },
};

describe("fmtTime", () => {
  it("formats 12-hour times", () => {
    expect(fmtTime(0)).toBe("12:00 am");
    expect(fmtTime(719)).toBe("11:59 am");
    expect(fmtTime(720)).toBe("12:00 pm");
    expect(fmtTime(1230)).toBe("8:30 pm");
    expect(fmtTime(1439)).toBe("11:59 pm");
  });
});

describe("toCalendarColumns", () => {
  const cols = toCalendarColumns(g, "2026-07-20");

  it("emits one column per date in range", () => {
    expect(cols.map((c) => c.date)).toEqual(["2026-07-20", "2026-07-21"]);
    expect(cols[0].dow).toBe("MON");
    expect(cols[1].dow).toBe("TUE");
  });

  it("marks today as Tonight", () => {
    expect(cols[0].isToday).toBe(true);
    expect(cols[0].sub).toBe("Tonight");
    expect(cols[1].isToday).toBe(false);
    expect(cols[1].sub).toBe("Jul 21");
  });

  it("keeps plan items only, sorted by start", () => {
    expect(cols[0].slots.map((s) => s.item.id)).toEqual([11, 12]);
    expect(cols[1].slots).toEqual([]);
  });

  it("resolves sidecars and builds subs", () => {
    const [movie, series] = cols[0].slots;
    expect(movie.title.name).toBe("Past Lives");
    expect(movie.timeLabel).toBe("8:00 pm");
    expect(movie.sub).toBe("Movie · Netflix");
    expect(series.sub).toBe("S2E5 · Apple TV+");
  });
});

describe("toBoardRows", () => {
  const day = toBoardRows(g, "2026-07-20");

  it("derives hour columns from plan items", () => {
    expect(day.times.map((t) => t.startMin)).toEqual([1200, 1260]);
    expect(day.times.map((t) => t.label)).toEqual(["8:00 pm", "9:00 pm"]);
  });

  it("numbers plan picks through the evening and dims alternates", () => {
    const apple = day.rows.find((r) => r.providerName === "Apple TV+")!;
    const netflix = day.rows.find((r) => r.providerName === "Netflix")!;
    // Netflix: plan movie at 8pm (step 1), alternate at 9:30→9pm column.
    expect(netflix.cells[0]).toMatchObject({ has: true, step: 1 });
    expect(netflix.cells[1]).toMatchObject({ has: true, step: null });
    // Apple: alternate at 8pm, plan series at 9:30→9pm column (step 2).
    expect(apple.cells[0]).toMatchObject({ has: true, step: null });
    expect(apple.cells[1]).toMatchObject({ has: true, step: 2 });
  });

  it("gives alternates a swap target (the plan item sharing their slot)", () => {
    const apple = day.rows.find((r) => r.providerName === "Apple TV+")!;
    const netflix = day.rows.find((r) => r.providerName === "Netflix")!;
    expect((apple.cells[0] as { swapTargetId?: number }).swapTargetId).toBe(11);
    expect((netflix.cells[1] as { swapTargetId?: number }).swapTargetId).toBe(12);
  });

  it("falls back for missing sidecar entries", () => {
    const netflix = day.rows.find((r) => r.providerName === "Netflix")!;
    const alt = netflix.cells[1];
    expect(alt.has && alt.title.name).toBe("Unknown title");
  });

  it("assembles the path from plan titles", () => {
    expect(day.path).toEqual(["Past Lives", "Severance"]);
  });

  it("returns empty structures for a night off", () => {
    const off = toBoardRows(g, "2026-07-21");
    expect(off.rows).toEqual([]);
    expect(off.path).toEqual([]);
  });
});
```

- [ ] **Step 4: RED** — `cd web && pnpm test` fails (module `./guide` missing).

- [ ] **Step 5: Implement** — create `web/src/lib/guide.ts`:

```ts
// Pure mappers from the guide payload to view models. No React, no
// fetching — vitest-covered. Dates are YYYY-MM-DD strings; the only Date
// use is UTC day-of-week/label derivation.

import type { GuideItem, GuideResponse, GuideTitleLookup } from "./types";

const DOW = ["SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"];
const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

const UNKNOWN_TITLE: GuideTitleLookup = { name: "Unknown title", kind: "movie", tmdb_id: 0 };

export function fmtTime(startMin: number): string {
  const h24 = Math.floor(startMin / 60) % 24;
  const m = startMin % 60;
  const suffix = h24 < 12 ? "am" : "pm";
  const h12 = h24 % 12 === 0 ? 12 : h24 % 12;
  return `${h12}:${String(m).padStart(2, "0")} ${suffix}`;
}

function utc(date: string): Date {
  return new Date(`${date}T00:00:00Z`);
}

function eachDate(start: string, end: string): string[] {
  const out: string[] = [];
  for (let t = utc(start).getTime(); t <= utc(end).getTime(); t += 86_400_000) {
    out.push(new Date(t).toISOString().slice(0, 10));
  }
  return out;
}

function titleOf(g: GuideResponse, item: GuideItem): GuideTitleLookup {
  return g.titles[String(item.title_id)] ?? UNKNOWN_TITLE;
}

function providerNameOf(g: GuideResponse, item: GuideItem): string {
  return g.providers[String(item.provider_id)]?.name ?? "";
}

function epLabel(title: GuideTitleLookup, item: GuideItem): string {
  return title.kind === "series" ? `S${item.season}E${item.episode}` : "Movie";
}

export type CalendarSlot = {
  item: GuideItem;
  title: GuideTitleLookup;
  providerName: string;
  timeLabel: string;
  sub: string;
};

export type CalendarColumn = {
  date: string;
  dow: string;
  sub: string;
  isToday: boolean;
  slots: CalendarSlot[];
};

export function toCalendarColumns(g: GuideResponse, today: string): CalendarColumn[] {
  const byDate = new Map<string, GuideItem[]>();
  for (const item of g.items) {
    if (!item.is_plan) continue;
    const list = byDate.get(item.date) ?? [];
    list.push(item);
    byDate.set(item.date, list);
  }
  return eachDate(g.start_date, g.end_date).map((date) => {
    const d = utc(date);
    const slots = (byDate.get(date) ?? [])
      .slice()
      .sort((a, b) => a.start_min - b.start_min)
      .map((item) => {
        const title = titleOf(g, item);
        const providerName = providerNameOf(g, item);
        return {
          item,
          title,
          providerName,
          timeLabel: fmtTime(item.start_min),
          sub: providerName ? `${epLabel(title, item)} · ${providerName}` : epLabel(title, item),
        };
      });
    return {
      date,
      dow: DOW[d.getUTCDay()],
      sub: date === today ? "Tonight" : `${MONTHS[d.getUTCMonth()]} ${d.getUTCDate()}`,
      isToday: date === today,
      slots,
    };
  });
}

export type BoardCell =
  | { has: false }
  | {
      has: true;
      item: GuideItem;
      title: GuideTitleLookup;
      sub: string;
      step: number | null;
      swapTargetId?: number;
    };

export type BoardRow = { providerId: number; providerName: string; cells: BoardCell[] };

export type BoardDay = {
  times: { startMin: number; label: string }[];
  rows: BoardRow[];
  path: string[];
};

export function toBoardRows(g: GuideResponse, date: string): BoardDay {
  const dayItems = g.items.filter((it) => it.date === date);
  const plan = dayItems
    .filter((it) => it.is_plan)
    .slice()
    .sort((a, b) => a.start_min - b.start_min);
  if (plan.length === 0) {
    return { times: [], rows: [], path: [] };
  }

  const hourOf = (startMin: number) => Math.floor(startMin / 60) * 60;
  const hours = [...new Set(plan.map((it) => hourOf(it.start_min)))].sort((a, b) => a - b);
  const stepOf = new Map(plan.map((it, i) => [it.id, i + 1]));
  const planBySlot = new Map(plan.map((it) => [it.start_min, it.id]));

  const providerIds = [...new Set(dayItems.map((it) => it.provider_id))];
  const rows: BoardRow[] = providerIds
    .map((pid) => {
      const cells: BoardCell[] = hours.map((hour) => {
        const candidates = dayItems
          .filter((it) => it.provider_id === pid && hourOf(it.start_min) === hour)
          .sort((a, b) => Number(b.is_plan) - Number(a.is_plan) || a.start_min - b.start_min);
        const item = candidates[0];
        if (!item) return { has: false };
        const title = titleOf(g, item);
        const step = item.is_plan ? (stepOf.get(item.id) ?? null) : null;
        const cell: BoardCell = {
          has: true,
          item,
          title,
          step,
          sub: `${epLabel(title, item)} · ${item.is_plan ? "your pick" : "alternate"}`,
        };
        if (!item.is_plan) {
          const target = planBySlot.get(item.start_min);
          if (target !== undefined) cell.swapTargetId = target;
        }
        return cell;
      });
      return {
        providerId: pid,
        providerName: g.providers[String(pid)]?.name ?? "",
        cells,
      };
    })
    .filter((row) => row.cells.some((c) => c.has))
    .sort((a, b) => a.providerName.localeCompare(b.providerName));

  return {
    times: hours.map((h) => ({ startMin: h, label: fmtTime(h) })),
    rows,
    path: plan.map((it) => titleOf(g, it).name),
  };
}
```

- [ ] **Step 6: GREEN** — `cd web && pnpm test` all green; then `pnpm lint && pnpm build` clean.

- [ ] **Step 7: Commit** — `feat(web): guide types and pure view mappers with vitest infra` (include package.json, lockfile, CI workflow, types, mapper, tests).

---

### Task 3: `/guide` page — GuideBody, GenerateBar, header (prose)

**Files:** Rewrite `web/src/app/guide/page.tsx` body → `web/src/app/guide/GuideBody.tsx` (+ keep page wrapper with AuthGate/Nav/Footer). Create `web/src/app/guide/GenerateBar.tsx`.

Per spec §"/guide page composition" and the reference's `isGuide` header block. `["guide"]` query on `api<GuideResponse>("/v1/guides/current")`, retry skipping 404 (title-page pattern); 404 → GenerateBar (panel card, heading `Plan your week`, date input default today — compute via the client's local date — days input 1–14 default 7, accent `Generate` pill → `POST /v1/guides {start_date, days}`, 422 → toast `Couldn't generate — check the dates.`, success → invalidate `["guide"]`); loading/error states in house style (cached-data-first). Guide present → header per reference: `Week of {Month D}`, summary `N evenings planned · M nights off` (singular `night off` when M=1, from `toCalendarColumns` slot presence), `Segmented` Calendar/Board (local state, calendar default), `↻ Regenerate remaining` pill → POST regenerate → invalidate + pinned toast. View bodies arrive in Tasks 4–5 — render the calendar placeholder `<CalendarView …/>`/`<BoardView …/>` split behind the toggle but implement CalendarView/BoardView as minimal stubs IN THIS TASK ONLY IF needed to keep the build green; otherwise structure so Tasks 4–5 fill dedicated files. Today's date string comes from a small `todayLocal()` helper (`new Date` local, YYYY-MM-DD) in GuideBody.

Gate + commit: `feat(web): guide page shell with generate bar and view toggle`

---

### Task 4: CalendarView + ItemMenu (prose)

**Files:** Create `web/src/app/guide/CalendarView.tsx`, `web/src/app/guide/ItemMenu.tsx`. Wire into GuideBody.

Per spec §CalendarView + §ItemMenu and the reference's calendar block. `toCalendarColumns(guide, today)`; desktop `lg:grid lg:grid-cols-7 gap-2`, below-lg horizontal snap scroll (`flex overflow-x-auto snap-x`, columns `min-w-[160px] snap-start`); day headers (dow 11px tracked, accent today; sub `Tonight`/date); slot cards per reference (panel rounded-xl; watched → opacity-50 + `✓ ` prefix; time/title/sub lines; `Pinned` acc-soft pill when pinned — one pill only, recorded divergence); `Night off` dashed box; card click toggles ONE open ItemMenu (state key `${date}-${itemId}` in GuideBody or CalendarView).

ItemMenu: panel2 chips per reference — `✓ Watched`, `Pin`/`Unpin`, `Swap`, `Move`, `Details`, `Remove`; all guide mutations in ONE `useMutation`-per-action or a small shared mutation helper inside ItemMenu (implementer's judgment; behavior contracts and toasts are pinned in Global Constraints); disabled while pending; watched invalidates `["guide"]`+`["shelf"]` prefix, everything else `["guide"]`; Swap expands an inline picker (rotation ∪ watchlist from `["shelf", …]` queries, dedupe by title_id, exclude the item's current title, loading `Loading…`/empty `Nothing to swap in yet.` lines) → PATCH `{title_id}`; 422 → pinned toast; Move expands day `<select>` (guide dates, DOW labels) + `<input type="time">` (defaults: item's date + fmt of start_min as HH:MM 24h) + `Move` chip → PATCH `{date, start_min}`; Details → `router.push` to `/title/{kind}/{tmdb_id}` from the calendar slot's resolved title; Remove → DELETE.

Gate (`pnpm lint && pnpm test && pnpm build`) + commit: `feat(web): guide calendar view with item actions`

---

### Task 5: BoardView (prose)

**Files:** Create `web/src/app/guide/BoardView.tsx`. Wire into GuideBody.

Per spec §BoardView and the reference's board block. Day chips (pill row from the guide's dates, DOW-cased labels, active ink/bg; default today-if-in-range else first date); heading `{Weekday} evening` (full weekday) + `Your path: {A → B → C}` or `Night off — nothing planned`; grid `[grid-template-columns:120px_repeat(n,1fr)]` inside `overflow-x-auto`; time header cells from `toBoardRows` labels; provider rows: name cell 12px semibold; plan cells `bg-panel border border-acc rounded-xl` with absolute step circle (`-top-2 left-3 bg-acc text-acc-ink w-[17px] h-[17px] rounded-full text-[10px] font-bold`) and sub `… · your pick` (`text-acc`); alternate cells `bg-panel2` muted with sub `… · alternate`; alternate click (only when `swapTargetId` present) PATCHes `{title_id: alternateItem.title_id}` on the swap target item → invalidate `["guide"]`, toast `Swapped in {title}`; caption `Numbered cards are your evening · tap any alternate to swap it in` (11.5px faint). Empty day → the heading + `Night off — nothing planned`, no grid.

Gate + commit: `feat(web): guide board view with tap-to-swap alternates`

---

### Task 6: Sweep + PR

- [ ] `cd web && pnpm lint && pnpm test && pnpm build`; `cd api && gofmt -l . && go vet ./... && TEST_DATABASE_URL=… go test ./... && go test ./...` — all green.
- [ ] Smoke on :3001: `/guide` 200. (The dev server hot-reloads; the API process must be RESTARTED for the sidecar fields — kill :8080 and relaunch per infra/local-dev.md before the user's manual pass.)
- [ ] Push + PR (`feat(web): guide calendar and board views with editing`, closes #18, writing-github-content style, note divergences: stacked cards decision, single Pinned pill, simple watched toast). User's manual loop is the acceptance gate.

## Execution notes

- Order strict 1→6. Task 1 needs lineup-pg for the store integration test.
- Tasks 3–5 implementers read spec + reference first; reference wins visuals.
- Final whole-branch review before Task 6's PR step.
