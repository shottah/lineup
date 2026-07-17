// Poster-derived card color + logo plate polarity (#47, docs/design/
// guide-card-redesign.md §2, §5). Pure hue/color math and cache
// orchestration live here and are vitest-covered; the actual canvas/
// Image sampling only runs in a browser (this repo's vitest config runs
// tests under `environment: "node"`, no jsdom) so it is isolated in
// `samplePosterHue`/`sampleLogoPlate` below and intentionally NOT unit
// tested — `posterHue`/`logoPlate` accept an injectable sampler so the
// surrounding cache/fallback/memoization behavior can be tested without
// a browser.
//
// A later task wires this into the guide views (a `usePosterHue` hook
// etc.); this module only owns the color machinery.

import { posterUrl } from "@/lib/tmdb";

const SAMPLE_SIZE = "w92" as const;

// --- §2.4 Deterministic fallback ---------------------------------------

// Knuth multiplicative hash: (title_id * 2654435761) % 360. Done in
// BigInt because title_id * 2654435761 exceeds Number's 2^53 safe-integer
// range for title_ids past ~3.4M — BigInt keeps the modulo exact
// regardless of title_id's magnitude; the result (0–359) always fits
// back into a plain Number.
export function hashHue(titleId: number): number {
  // BigInt() calls, not `n` literals — the project's ES2017 build target
  // doesn't support BigInt literal syntax.
  return Number((BigInt(titleId) * BigInt(2654435761)) % BigInt(360));
}

// --- §1 hue/token composition -------------------------------------------

// The base tint color: hue is a concrete value (already resolved via
// hashHue or posterHue), s/l stay as the CSS custom properties defined
// per-theme in globals.css so the same string adapts across themes.
export function tintFromHue(hue: number): string {
  return `hsl(${hue} var(--tint-s) var(--tint-l))`;
}

// --- §2.3 steps 3–6: pixel → hue color math (pure, DOM-free) ------------

export type Rgb = { r: number; g: number; b: number };
export type Hsl = { h: number; s: number; l: number };

// Standard RGB→HSL conversion. r/g/b in [0,255]; h in [0,360), s/l in [0,1].
export function rgbToHsl(r: number, g: number, b: number): Hsl {
  const rn = r / 255;
  const gn = g / 255;
  const bn = b / 255;
  const max = Math.max(rn, gn, bn);
  const min = Math.min(rn, gn, bn);
  const l = (max + min) / 2;

  if (max === min) {
    return { h: 0, s: 0, l };
  }

  const d = max - min;
  const s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
  let h: number;
  switch (max) {
    case rn:
      h = (gn - bn) / d + (gn < bn ? 6 : 0);
      break;
    case gn:
      h = (bn - rn) / d + 2;
      break;
    default:
      h = (rn - gn) / d + 4;
      break;
  }
  return { h: (h * 60 + 360) % 360, s, l };
}

// Circular mean of hue angles (degrees): average each hue as a unit
// vector and atan2 back, rather than a plain arithmetic mean, which is
// wrong across the 359°→0° wraparound (spec §2.3 step 6).
export function circularMeanHue(hues: number[]): number {
  let sumSin = 0;
  let sumCos = 0;
  for (const h of hues) {
    const rad = (h * Math.PI) / 180;
    sumSin += Math.sin(rad);
    sumCos += Math.cos(rad);
  }
  const meanRad = Math.atan2(sumSin, sumCos);
  const meanDeg = (meanRad * 180) / Math.PI;
  return (meanDeg + 360) % 360;
}

// Filters near-gray/extreme-lightness pixels (§2.3 step 4), abandons
// (returns null) when fewer than 10% survive (step 5), otherwise returns
// the circular mean hue of the survivors (step 6), rounded to an integer
// per §1's "hue is an integer 0–359" contract.
export function dominantHue(pixels: Rgb[]): number | null {
  if (pixels.length === 0) return null;

  const survivorHues: number[] = [];
  for (const { r, g, b } of pixels) {
    const { h, s, l } = rgbToHsl(r, g, b);
    if (s < 0.15) continue;
    if (l < 0.08 || l > 0.92) continue;
    survivorHues.push(h);
  }

  if (survivorHues.length / pixels.length < 0.1) return null;
  return Math.round(circularMeanHue(survivorHues)) % 360;
}

// --- §5.1 steps 4–5: logo luminance → plate polarity (pure, DOM-free) ---

export type Rgba = { r: number; g: number; b: number; a: number };

// Alpha-weighted mean luminance (0–1) over pixels with alpha > 10 (skips
// near-fully-transparent padding). Alpha itself is left at its native
// 0–255 scale — it only ever appears as a weight in a ratio, so its
// scale cancels out.
export function meanLuminance(pixels: Rgba[]): number {
  let weightedSum = 0;
  let totalWeight = 0;
  for (const { r, g, b, a } of pixels) {
    if (a <= 10) continue;
    const luminance = 0.2126 * (r / 255) + 0.7152 * (g / 255) + 0.0722 * (b / 255);
    weightedSum += luminance * a;
    totalWeight += a;
  }
  return totalWeight === 0 ? 0 : weightedSum / totalWeight;
}

export function classifyPlate(meanLum: number): "plate-light" | "plate-dark" {
  return meanLum > 0.75 ? "plate-dark" : "plate-light";
}

// --- §2.3: browser-only poster sampling ---------------------------------

// Draws the poster to an offscreen 12×18 canvas (2:3, matches poster
// aspect — the browser's downscale filtering is itself the
// noise-reduction step, per §2.3 step 2), reads all 216 pixels, and
// resolves the dominant hue. Requires a real DOM (Image, canvas,
// getImageData) — not exercised under vitest's node environment, so it
// is not covered by this file's test suite; dominantHue/rgbToHsl/
// circularMeanHue above carry the actual color-math test coverage.
async function samplePosterHue(url: string): Promise<number> {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.crossOrigin = "anonymous";
    img.onload = () => {
      try {
        const canvas = document.createElement("canvas");
        canvas.width = 12;
        canvas.height = 18;
        const ctx = canvas.getContext("2d");
        if (!ctx) throw new Error("2d canvas context unavailable");
        ctx.drawImage(img, 0, 0, 12, 18);
        const { data } = ctx.getImageData(0, 0, 12, 18);
        const pixels: Rgb[] = [];
        for (let i = 0; i < data.length; i += 4) {
          pixels.push({ r: data[i], g: data[i + 1], b: data[i + 2] });
        }
        const hue = dominantHue(pixels);
        if (hue === null) {
          reject(new Error("insufficient color signal to extract a poster hue"));
          return;
        }
        resolve(hue);
      } catch (err) {
        reject(err instanceof Error ? err : new Error(String(err)));
      }
    };
    img.onerror = () => reject(new Error(`failed to load poster image: ${url}`));
    img.src = url;
  });
}

// --- §5.1: browser-only logo sampling -----------------------------------

// Draws the logo (stretched — shape doesn't matter for a luminance read,
// only tone does, per §5.1 step 3) to an offscreen 8×8 canvas and
// resolves its plate polarity. Not covered by this file's test suite for
// the same DOM reasons as samplePosterHue above.
async function sampleLogoPlate(url: string): Promise<"plate-light" | "plate-dark"> {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.crossOrigin = "anonymous";
    img.onload = () => {
      try {
        const canvas = document.createElement("canvas");
        canvas.width = 8;
        canvas.height = 8;
        const ctx = canvas.getContext("2d");
        if (!ctx) throw new Error("2d canvas context unavailable");
        ctx.drawImage(img, 0, 0, 8, 8);
        const { data } = ctx.getImageData(0, 0, 8, 8);
        const pixels: Rgba[] = [];
        for (let i = 0; i < data.length; i += 4) {
          pixels.push({ r: data[i], g: data[i + 1], b: data[i + 2], a: data[i + 3] });
        }
        resolve(classifyPlate(meanLuminance(pixels)));
      } catch (err) {
        reject(err instanceof Error ? err : new Error(String(err)));
      }
    };
    img.onerror = () => reject(new Error(`failed to load logo image: ${url}`));
    img.src = url;
  });
}

// --- §2.2 cache + orchestration ------------------------------------------

type PosterHueEntry = { hue: number; source: "extracted" | "fallback" };

// Module-level, in-memory, keyed by title_id (not poster_path — every
// card for the same title across the week shares one color). Only
// resolved (extracted-or-permanently-fallback) results live here;
// `inFlight` dedupes concurrent extraction attempts for the same
// title_id so a title appearing in several slots at once triggers only
// one image decode.
const posterHueCache = new Map<number, PosterHueEntry>();
const posterHueInFlight = new Map<number, Promise<number>>();

// Resolves the poster-derived hue for a title, extracting at most once
// per title_id for the life of the session. On any failure (missing
// poster_path, load error, decode error, or too little color signal —
// see dominantHue) resolves to the deterministic hashHue fallback and
// caches that outcome so it is never retried. `extract` is injectable
// (defaults to the real canvas sampler) so this orchestration is
// testable without a browser.
export async function posterHue(
  titleId: number,
  posterPath: string,
  extract: (url: string) => Promise<number> = samplePosterHue,
): Promise<number> {
  const cached = posterHueCache.get(titleId);
  if (cached) return cached.hue;

  const inFlight = posterHueInFlight.get(titleId);
  if (inFlight) return inFlight;

  const fallback = hashHue(titleId);
  const src = posterUrl(posterPath, SAMPLE_SIZE);
  if (!src) {
    posterHueCache.set(titleId, { hue: fallback, source: "fallback" });
    return fallback;
  }

  const attempt = extract(src)
    .then((hue) => {
      posterHueCache.set(titleId, { hue, source: "extracted" });
      return hue;
    })
    .catch(() => {
      posterHueCache.set(titleId, { hue: fallback, source: "fallback" });
      return fallback;
    })
    .finally(() => {
      posterHueInFlight.delete(titleId);
    });

  posterHueInFlight.set(titleId, attempt);
  return attempt;
}

// Synchronous read of the resolved poster hue cache — never triggers
// extraction and never observes a still-pending `posterHue` call, even if
// one is in flight for this title_id. Lets a first render read an
// already-resolved value synchronously (spec's zero-flash pattern:
// hash-first synchronously, silent canvas upgrade once `posterHue`
// resolves) instead of always waiting a tick for the async path.
export function peekPosterHue(titleId: number): number | null {
  return posterHueCache.get(titleId)?.hue ?? null;
}

// --- §5.1 cache + orchestration -------------------------------------------

export type LogoPlate = "plate-light" | "plate-dark" | "text-fallback";

// Keyed by provider_id per §5.1 step 1 (there are only a handful of
// distinct providers across any guide, so this is essentially free after
// first use) — deliberately not keyed by logo_path, mirroring
// posterHueCache's title_id keying above.
const logoPlateCache = new Map<number, LogoPlate>();
const logoPlateInFlight = new Map<number, Promise<LogoPlate>>();

// Resolves the plate polarity for a provider's logo mark, sampling at
// most once per provider_id. Empty logo_path skips sampling entirely
// (no network request, §5.1 step 7). Any load/decode/tainted-canvas
// failure resolves to "text-fallback", cached so it isn't retried —
// callers render the plain-text provider name in that case (§5.5).
// `sample` is injectable (defaults to the real canvas sampler) for the
// same testability reason as posterHue.
export async function logoPlate(
  providerId: number,
  logoPath: string,
  sample: (url: string) => Promise<"plate-light" | "plate-dark"> = sampleLogoPlate,
): Promise<LogoPlate> {
  const cached = logoPlateCache.get(providerId);
  if (cached) return cached;

  const inFlight = logoPlateInFlight.get(providerId);
  if (inFlight) return inFlight;

  const src = posterUrl(logoPath, SAMPLE_SIZE);
  if (!src) {
    logoPlateCache.set(providerId, "text-fallback");
    return "text-fallback";
  }

  const attempt: Promise<LogoPlate> = sample(src)
    .then((plate): LogoPlate => {
      logoPlateCache.set(providerId, plate);
      return plate;
    })
    .catch((): LogoPlate => {
      logoPlateCache.set(providerId, "text-fallback");
      return "text-fallback";
    })
    .finally(() => {
      logoPlateInFlight.delete(providerId);
    });

  logoPlateInFlight.set(providerId, attempt);
  return attempt;
}

// Synchronous read of the resolved logo plate cache — same semantics as
// peekPosterHue above: never triggers sampling, never observes a
// still-pending `logoPlate` call.
export function peekLogoPlate(providerId: number): LogoPlate | null {
  return logoPlateCache.get(providerId) ?? null;
}
