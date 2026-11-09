// @vitest-environment jsdom
// New agent's recent setups (#2134) against the rules every client shares (spec/fixtures/recents): every case of record,
// cards and a machine's model, then this browser's list.
import { beforeEach, describe, expect, it } from "vitest";
import fixture from "../../../spec/fixtures/recents/recent_setups.json";
import {
  clearRecentSetups, forgetSetup, formHolds, loadRecentSetups, machineModel, MAX_RECENT_CARDS, MAX_RECENT_SETUPS, recentCards, recentSetupsKey,
  recordSetup, rememberSetup, type RecentSetup,
} from "./recent-setups";

type Case = { name: string; operation: string; input: Record<string, unknown>; expected: Record<string, unknown> };
const cases = fixture.cases as Case[];
const of = (operation: string) => cases.filter((c) => c.operation === operation);

describe("recent setups (spec/fixtures/recents)", () => {
  it("has the fixture's limits", () => {
    expect(MAX_RECENT_SETUPS).toBe(fixture.max_entries);
    expect(MAX_RECENT_CARDS).toBe(fixture.max_cards);
  });

  it("knows every operation the fixture has", () => {
    expect(new Set(cases.map((c) => c.operation))).toEqual(new Set(["record", "cards", "model"]));
  });

  describe("record", () => {
    for (const c of of("record")) {
      it(c.name, () => expect(recordSetup(c.input.before as RecentSetup[], c.input.setup as RecentSetup)).toEqual(c.expected.after));
    }
  });

  describe("cards", () => {
    for (const c of of("cards")) {
      it(c.name, () => {
        const cards = recentCards(c.input.entries as RecentSetup[], c.input.machines as { id: string; online: boolean }[]);
        expect(cards.map(({ index, online }) => ({ index, online }))).toEqual(c.expected.cards);
        expect(cards.map((card) => card.setup)).toEqual(cards.map((card) => (c.input.entries as RecentSetup[])[card.index]));
      });
    }
  });

  describe("a machine's model", () => {
    for (const c of of("model")) {
      it(c.name, () => expect(machineModel(c.input.entries as RecentSetup[], c.input.machine as string, c.input.launcher as string)).toBe(c.expected.model));
    }
  });
});

const SETUP: RecentSetup = { machine: "mac-a", cwd: "/code/persona", launcher: "", model: "opus", agent_type: "claude", mode: "plan", effort: "high", at: "2026-11-09T03:00:00Z" };
const CODEX: RecentSetup = { machine: "home-c", cwd: "/code/data-pipeline", launcher: "codex", model: "gpt-5.5", agent_type: "codex", mode: "", effort: "", at: "2026-11-09T01:00:00Z" };

describe("this browser's list", () => {
  beforeEach(() => localStorage.clear());

  it("remembers, forgets and clears", () => {
    rememberSetup(CODEX);
    rememberSetup(SETUP);
    expect(loadRecentSetups()).toEqual([SETUP, CODEX]);
    rememberSetup({ ...CODEX, at: "2026-11-09T06:00:00Z" });
    expect(loadRecentSetups().map((e) => e.machine)).toEqual(["home-c", "mac-a"]);
    forgetSetup({ ...SETUP, at: "whenever" }); // the same setup, whatever its time
    expect(loadRecentSetups()).toEqual([{ ...CODEX, at: "2026-11-09T06:00:00Z" }]);
    clearRecentSetups();
    expect(loadRecentSetups()).toEqual([]);
    expect(localStorage.getItem(recentSetupsKey())).toBeNull();
  });

  it("keeps only well-formed setups and only their fields", () => {
    localStorage.setItem(recentSetupsKey(), JSON.stringify([{ ...SETUP, prompt: "never kept" }, { machine: "x" }, "junk", null]));
    expect(loadRecentSetups()).toEqual([SETUP]);
    localStorage.setItem(recentSetupsKey(), "{not json");
    expect(loadRecentSetups()).toEqual([]);
  });

  it("keeps one list per hub sub-path", () => {
    expect(recentSetupsKey("")).toBe("persona.recentSetups");
    expect(recentSetupsKey("/hub-b")).toBe("persona.recentSetups./hub-b");
  });
});

describe("formHolds", () => {
  const form = { machine: "mac-a", cwd: "/code/persona", launcher: "", model: "opus", mode: "plan", effort: "high" };
  it("is what the card put in", () => {
    expect(formHolds(SETUP, form)).toBe(true);
    expect(formHolds(SETUP, { ...form, cwd: "/code/other" })).toBe(false);
    expect(formHolds(SETUP, { ...form, model: "" })).toBe(false);
    expect(formHolds(SETUP, { ...form, effort: "" })).toBe(false);
  });
  it("leaves mode and effort out for an agent that has none", () => {
    expect(formHolds(CODEX, { machine: "home-c", cwd: "/code/data-pipeline", launcher: "codex", model: "gpt-5.5", mode: "plan", effort: "" })).toBe(true);
  });
});
