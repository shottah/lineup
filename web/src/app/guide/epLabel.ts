// Season/episode label for a calendar slot's sub-line (design spec §5.3):
// the sub-line changes from lib/guide.ts's pre-joined `sub` string to a
// flex row built from this label plus a separate provider chip, so
// CalendarView needs the label available on its own. Mirrors lib/guide.ts's
// private epLabel exactly — kept here rather than exported from lib/guide.ts
// to avoid touching that frozen module for this task.

import type { GuideItem, GuideTitleLookup } from "@/lib/types";

export function epLabel(title: GuideTitleLookup, item: GuideItem): string {
  return title.kind === "series" ? `S${item.season}E${item.episode}` : "Movie";
}
