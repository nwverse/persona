// @vitest-environment jsdom
// The Agents pages' GitHub star prompts (#5761): the empty start area's pill and the milestone line in the hub notice's slot.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { t } from "../../i18n";
import { getStarPrompt, PERSONA_REPO_URL } from "../../lib/star-prompt";
import { getState } from "../../lib/store";
import type { Interaction, Session } from "../../lib/types";
import { StarPill, StarPromptLine } from "./StarPrompt";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement;
let root: Root;

const seed = (state: object) => localStorage.setItem("persona.starPrompt", JSON.stringify(state));
const render = (node: React.ReactNode) => act(() => root.render(node));
const q = (id: string) => host.querySelector<HTMLElement>(`[data-testid="${id}"]`);

beforeEach(() => {
  localStorage.clear();
  Object.assign(getState(), { sessions: {}, interactions: [] });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("the empty start area's pill", () => {
  it("links to the project in a new tab and counts a click as starred", () => {
    render(<StarPill />);
    const link = q("star-pill")!.querySelector("a")!;
    expect(link.textContent).toBe(t("lib-star-prompt.star"));
    expect(link.getAttribute("href")).toBe(PERSONA_REPO_URL);
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("rel")).toBe("noopener noreferrer");
    link.addEventListener("click", (e) => e.preventDefault());
    act(() => link.click());
    expect(getStarPrompt().done).toBe(true);
    expect(q("star-pill")).toBeNull();
  });

  it("hides for good with ✕", () => {
    render(<StarPill />);
    expect(q("star-pill-hide")!.getAttribute("aria-label")).toBe(t("lib-star-prompt.hide"));
    act(() => q("star-pill-hide")!.click());
    expect(q("star-pill")).toBeNull();
    expect(getStarPrompt().pillHidden).toBe(true);
  });
});

describe("the milestone line", () => {
  it("waits for the threshold", () => {
    seed({ launches: 19 });
    render(<StarPromptLine />);
    expect(q("star-prompt")).toBeNull();
  });

  it("shows from the threshold, and Later doubles it and waits", () => {
    seed({ launches: 20 });
    render(<StarPromptLine />);
    expect(q("star-prompt")!.textContent).toContain(t("lib-star-prompt.card"));
    expect(q("star-prompt-star")!.getAttribute("href")).toBe(PERSONA_REPO_URL);
    act(() => q("star-prompt-later")!.click());
    expect(q("star-prompt")).toBeNull();
    expect(getStarPrompt()).toMatchObject({ threshold: 40 });
    expect(getStarPrompt().snoozedUntil).toBeGreaterThan(Date.now());
  });

  it("stays away while a session needs the user, a card waits or a search is on", () => {
    seed({ launches: 20 });
    act(() => Object.assign(getState(), { sessions: { m1: [{ machine_id: "m1", session_id: "s1", state: "needs_you" } as Session] } }));
    render(<StarPromptLine />);
    expect(q("star-prompt")).toBeNull();
    act(() => Object.assign(getState(), { sessions: {}, interactions: [{ id: "ix1", machine_id: "m1" } as Interaction] }));
    render(<StarPromptLine key="cards" />);
    expect(q("star-prompt")).toBeNull();
    act(() => Object.assign(getState(), { interactions: [] }));
    render(<StarPromptLine key="search" searching />);
    expect(q("star-prompt")).toBeNull();
    render(<StarPromptLine key="idle" />);
    expect(q("star-prompt")).not.toBeNull();
  });

  it("is gone once starred", () => {
    seed({ launches: 50, done: true });
    render(<StarPromptLine />);
    expect(q("star-prompt")).toBeNull();
  });
});
