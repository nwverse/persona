// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import fixture from "../../../spec/fixtures/star-prompt/cases.json";
import type { Readiness, ReadinessStep } from "./readiness";
import {
  getStarPrompt, PERSONA_REPO_URL, setupFinished, showsStarCard, showsStarPill, starOnGitHub, starPromptAfter, starPromptLaunched,
  starPromptReadiness, STAR_PROMPT_START, type StarPromptEvent, type StarPromptState,
} from "./star-prompt";

const toast = vi.hoisted(() => vi.fn());
vi.mock("../ui/atoms", () => ({ toast }));

// Android (StarPromptFixtureTest) replays the same cases; iOS will with #5910.
describe("when the GitHub star prompts show (spec/fixtures/star-prompt)", () => {
  for (const c of fixture.cases) {
    it(c.name, () => {
      const { state, toast: shown } = c.event
        ? starPromptAfter(c.state as StarPromptState, c.event as StarPromptEvent, c.now)
        : { state: c.state as StarPromptState, toast: false };
      expect(state).toEqual(c.expected.state);
      expect(showsStarPill(state)).toBe(c.expected.pill);
      expect(showsStarCard(state, c.now)).toBe(c.expected.card);
      expect(shown).toBe(c.expected.toast);
    });
  }
});

const step = (id: string, state: ReadinessStep["state"]) => ({ id, state }) as ReadinessStep;
const readiness = (ready: boolean, first: ReadinessStep["state"]): Readiness =>
  ({ ready, mode: ready ? "full" : "setup", steps: [step("computer", ready ? "ok" : "todo"), step("first_message", first)] }) as unknown as Readiness;

describe("setup finished", () => {
  it("is setup turning ready before the first answer", () => {
    expect(setupFinished(readiness(false, "todo"), readiness(true, "todo"))).toBe(true);
  });
  it("is the first answer arriving after setup", () => {
    expect(setupFinished(readiness(true, "todo"), readiness(true, "ok"))).toBe(true);
  });
  it("is not a long-time user's computer coming back", () => {
    expect(setupFinished(readiness(false, "ok"), readiness(true, "ok"))).toBe(false);
  });
  it("is not the first answer this tab reads", () => {
    expect(setupFinished(null, readiness(true, "todo"))).toBe(false);
    expect(setupFinished(undefined, readiness(true, "ok"))).toBe(false);
  });
  it("is not still being set up", () => {
    expect(setupFinished(readiness(false, "todo"), readiness(false, "todo"))).toBe(false);
  });
});

describe("this browser's state", () => {
  beforeEach(() => {
    localStorage.clear();
    toast.mockClear();
  });
  afterEach(() => vi.restoreAllMocks());

  it("starts at the starting state and counts launches", () => {
    expect(getStarPrompt()).toEqual(STAR_PROMPT_START);
    starPromptLaunched();
    starPromptLaunched();
    expect(getStarPrompt().launches).toBe(2);
  });

  it("reads a damaged value as the starting state", () => {
    localStorage.setItem("persona.starPrompt", "{not json");
    expect(getStarPrompt()).toEqual(STAR_PROMPT_START);
    localStorage.setItem("persona.starPrompt", JSON.stringify({ launches: -3, threshold: 0, done: "yes" }));
    expect(getStarPrompt()).toEqual(STAR_PROMPT_START);
  });

  it("shows the toast once when setup finishes, with Star opening the project in a new tab", () => {
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    starPromptReadiness(readiness(false, "todo"), readiness(true, "todo"));
    starPromptReadiness(readiness(false, "todo"), readiness(true, "todo"));
    expect(toast).toHaveBeenCalledTimes(1);
    const [, tone, action] = toast.mock.calls[0];
    expect(tone).toBe("info");
    expect(action.sticky).toBe(true);
    action.run();
    expect(open).toHaveBeenCalledWith(PERSONA_REPO_URL, "_blank", "noopener,noreferrer");
    expect(getStarPrompt()).toMatchObject({ done: true, welcomed: true });
  });

  it("shows no toast to a long-time user", () => {
    starPromptReadiness(readiness(false, "ok"), readiness(true, "ok"));
    expect(toast).not.toHaveBeenCalled();
    expect(getStarPrompt().welcomed).toBe(false);
  });

  it("Star from a button turns every prompt off", () => {
    vi.spyOn(window, "open").mockReturnValue(null);
    starOnGitHub();
    const s = getStarPrompt();
    expect(showsStarPill(s)).toBe(false);
    expect(showsStarCard({ ...s, launches: 100 }, Date.now())).toBe(false);
  });
});
