import { t } from "../i18n";
import type { Star } from "./starred-rows";

export function starSpeaker(star: Star): string {
  return star.speaker || t(star.role === "user" ? "starred.you" : star.conversation === "agent" ? "starred.agent" : "starred.assistant");
}

function searchText(value: string): string {
  return value.normalize("NFKC").toLocaleLowerCase();
}

export function searchStars(stars: readonly Star[], query: string): readonly Star[] {
  const terms = searchText(query.trim()).split(/\s+/).filter(Boolean);
  if (!terms.length) return stars;
  return stars.filter((star) => {
    const value = searchText(`${star.text}\n${star.title}\n${starSpeaker(star)}`);
    return terms.every((term) => value.includes(term));
  });
}

export type SourceFilter = "all" | Star["conversation"];

export function filterStars(stars: readonly Star[], query: string, source: SourceFilter, collection: string, assignments: Readonly<Record<string, string>>): readonly Star[] {
  const filtered = source === "all" && collection === "all" ? stars : stars.filter((star) =>
    (source === "all" || star.conversation === source) &&
    (collection === "all" || (collection === "unfiled" ? !assignments[star.id] : assignments[star.id] === collection))
  );
  return searchStars(filtered, query);
}
