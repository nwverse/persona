import { expect, it } from "vitest";
import { filterStars } from "./starred-filter";
import type { Star } from "./starred-rows";

const first: Star = {
  id: "one", conversation: "assistant", machine_id: "one", session_id: "assistant", item_id: "reply",
  role: "assistant", speaker: "Persona", title: "Release plan", text: "Ｃheck the build report.", starred_at: "2026-11-21T00:00:00Z",
};
const second: Star = {
  ...first, id: "two", conversation: "agent", session_id: "agent", speaker: "Coder", title: "Release",
  text: "Run checks before upload.",
};
const third: Star = {
  ...second, id: "three", title: "Research", text: "Release notes need a second look.",
};
const stars = [first, second, third];
const assignments = { one: "review", two: "review" };

it("combines collection and source with normalized snapshot search", () => {
  expect(filterStars(stars, " release CHECK ", "all", "review", assignments)).toEqual([first, second]);
  expect(filterStars(stars, "release build", "assistant", "review", assignments)).toEqual([first]);
  expect(filterStars(stars, "coder release", "agent", "unfiled", assignments)).toEqual([third]);
  expect(filterStars(stars, "release", "assistant", "unfiled", assignments)).toEqual([]);
});

it("returns all stars in their original order without filters", () => {
  expect(filterStars(stars, "", "all", "all", assignments)).toBe(stars);
});
