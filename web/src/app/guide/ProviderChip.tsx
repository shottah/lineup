/* eslint-disable @next/next/no-img-element -- plain <img> is deliberate:
   TMDB w92 logo marks are pre-sized; next/image would add remotePatterns
   config and an optimization hop for CDN-optimized files (docs/design/
   guide-card-redesign.md §5). */
"use client";

import { useEffect, useState } from "react";

import { posterUrl } from "@/lib/tmdb";

import { logoPlate, peekLogoPlate, type LogoPlate } from "./posterTint";

const PLATE_BG: Record<"plate-light" | "plate-dark", string> = {
  "plate-light": "bg-[#EDEAE3]",
  "plate-dark": "bg-[#22252C]",
};

// Same zero-flash shape as usePosterHue (including the render-phase
// resync for a reused instance, rather than a synchronous setState in the
// effect), but there is no deterministic fallback for a logo: an
// unresolved/failed sample just keeps rendering the text fallback (§5.5),
// which is already the synchronous default (peekLogoPlate returns null
// until something resolves).
function useLogoPlate(providerId: number, logoPath: string): LogoPlate | null {
  const [resolvedFor, setResolvedFor] = useState(providerId);
  const [plate, setPlate] = useState<LogoPlate | null>(() => peekLogoPlate(providerId));
  if (providerId !== resolvedFor) {
    setResolvedFor(providerId);
    setPlate(peekLogoPlate(providerId));
  }

  useEffect(() => {
    let cancelled = false;
    logoPlate(providerId, logoPath).then((resolved) => {
      if (!cancelled) setPlate(resolved);
    });
    return () => {
      cancelled = true;
    };
  }, [providerId, logoPath]);

  return plate;
}

// Provider identity per design spec §5: a small branded logo chip in a
// neutral plate whose polarity is sampled from the logo's own pixels
// (independent of app theme), falling back to the plain provider-name
// text whenever there's no logo_path, the sample hasn't resolved yet, or
// sampling failed (§5.5 fallback chain — text is always the safety net).
// `variant` selects the calendar sub-line's inline chip (§5.3, embedded
// in a sentence, name carried by the <img alt>) vs. the board row
// header's standalone chip (§5.4, its own role="img"/aria-label since it
// isn't embedded in text).
export function ProviderChip({
  variant,
  providerId,
  logoPath,
  providerName,
}: {
  variant: "inline" | "standalone";
  providerId: number;
  logoPath: string;
  providerName: string;
}) {
  const plate = useLogoPlate(providerId, logoPath);

  // No name means no accessible label to give the standalone chip's
  // role="img" — rather than emit aria-label="", fall through to the
  // same text fallback used when the plate hasn't resolved (empty text
  // for an empty name, i.e. renders nothing).
  if ((plate !== "plate-light" && plate !== "plate-dark") || !providerName) {
    return variant === "inline" ? <span>{providerName}</span> : <>{providerName}</>;
  }

  const logoUrl = posterUrl(logoPath, "w92") ?? "";
  const bg = PLATE_BG[plate];

  if (variant === "inline") {
    return (
      <span
        className={`inline-flex h-4 w-4 shrink-0 items-center justify-center rounded-[5px] border border-line ${bg}`}
      >
        <img
          src={logoUrl}
          alt={providerName}
          title={providerName}
          className="h-[11px] w-[11px] object-contain"
        />
      </span>
    );
  }

  return (
    <span
      role="img"
      aria-label={providerName}
      title={providerName}
      className={`inline-flex h-[22px] w-[22px] shrink-0 items-center justify-center rounded-[6px] border border-line ${bg}`}
    >
      <img src={logoUrl} alt="" className="h-3.5 w-3.5 object-contain" />
    </span>
  );
}
