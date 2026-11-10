// @vitest-environment jsdom
// A `managers` push or a hello while a roster read is out: that read may have been answered before the change, so the
// roster is read once more after it, and callers that wait see the newest answer.
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { resetManagerChoice } from "./manager-choice";
import { currentRoster, loadRoster, resetRoster } from "./managers";

const older = [{ id: "m-1", name: "Work", online: true }];
const newer = [...older, { id: "m-2", name: "Home", online: true }];
let reads = 0;
let release!: () => void;

beforeEach(() => {
  resetRoster();
  resetManagerChoice();
  reads = 0;
  const gate = new Promise<void>((r) => (release = r));
  vi.stubGlobal("fetch", vi.fn(async () => {
    const mine = ++reads;
    if (mine === 1) await gate;
    const managers = mine === 1 ? older : newer;
    return { ok: true, status: 200, text: async () => JSON.stringify({ default: "m-1", managers }), headers: new Headers() };
  }));
});

afterEach(() => {
  vi.unstubAllGlobals();
  resetRoster();
  resetManagerChoice();
});

const ids = () => currentRoster()?.managers.map((m) => m.id);

it("a push while a read is out reads again after it, and both callers wait for the newer answer", async () => {
  const first = loadRoster();
  await Promise.resolve();
  const pushed = loadRoster(); // the hub added m-2 after the first read was answered on its side
  release();
  await first;
  expect(ids()).toEqual(["m-1", "m-2"]);
  await pushed;
  expect(ids()).toEqual(["m-1", "m-2"]);
  expect(reads).toBe(2);
});

it("several pushes while a read is out make one more read, not one each", async () => {
  const first = loadRoster();
  await Promise.resolve();
  void loadRoster();
  void loadRoster();
  void loadRoster();
  release();
  await first;
  expect(reads).toBe(2);
  expect(ids()).toEqual(["m-1", "m-2"]);
});

it("a read nobody pushed during is read once", async () => {
  const first = loadRoster();
  release();
  await first;
  expect(reads).toBe(1);
  expect(ids()).toEqual(["m-1"]);
  await loadRoster();
  expect(reads).toBe(2);
});
