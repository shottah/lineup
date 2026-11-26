import { offerLabel } from "@/lib/guide";
import type { GuideItem } from "@/lib/types";

// Small "Rent" / "Buy" tag for items that cost extra on their provider
// (the API falls back to rental/purchase providers when a title has no
// subscription home in the region). Streaming items render nothing.
export function OfferBadge({ item }: { item: GuideItem }) {
  const label = offerLabel(item);
  if (label === null) return null;
  return (
    <span
      title={label === "Rent" ? "Rental: costs extra" : "Purchase: costs extra"}
      className="shrink-0 rounded-full border border-line px-1.5 text-[9.5px] leading-[15px] font-semibold tracking-[.02em] text-ink"
    >
      {label}
    </span>
  );
}
