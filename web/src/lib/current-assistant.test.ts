// @vitest-environment jsdom
// The current assistant (docs/assistant/managers.md "Clients"): the pick is kept in this browser and survives a reload, a
// manager that left the roster goes back to the default, the pages list only the current one's things, and each
// assistant's count is its agents waiting plus its inbox items that need the user.
import { afterEach, describe, expect, it, vi } from "vitest";
import { scoped, scopeOf } from "./current-assistant";
import { managerBadges, waitingAgents } from "./manager-attention";
import { CHOICE_KEY, chooseManager, chosenManager, currentHeaders, managerHeaderFor, resetManagerChoice, shownManager } from "./manager-choice";
import { avatarColor, avatarInitial, resetRoster, setRoster, switcherLine, type ManagerList } from "./managers";
import { routeAfterSwitch } from "./persona-nav";
import type { Interaction, Session } from "./types";

afterEach(() => {
  resetRoster();
  resetManagerChoice();
});

const ROSTER: ManagerList = {
  default: "assistant",
  managers: [
    { id: "assistant", name: "助手", online: true, builtin: true, host: "server", inbox_needs_you: 4 },
    { id: "m-1", name: "工作", online: true, host: "MacBook", inbox_needs_you: 2 },
    { id: "m-2", name: "家里", online: false, last_seen: new Date(Date.now() - 3 * 3600_000).toISOString() },
    { id: "m-3", name: "测试", online: false, installing: new Date(Date.now() + 8 * 60_000).toISOString() },
  ],
};
const ONE: ManagerList = { default: "assistant", managers: [ROSTER.managers[0]] };

const session = (id: string, manager: string | undefined, patch: Partial<Session> = {}): Session => ({ machine_id: "mac", session_id: id, state: "needs_you", manager, ...patch }) as Session;
const card = (id: string, manager: string | undefined, sid: string): Interaction => ({ id, manager, machine_id: "mac", session_id: sid, kind: "question" }) as Interaction;

describe("the pick", () => {
  it("is kept in this browser, and only when it is another assistant than the default", () => {
    setRoster(ROSTER);
    chooseManager("m-1");
    expect(localStorage.getItem(CHOICE_KEY)).toBe("m-1");
    expect(managerHeaderFor("/assistant")).toBe("m-1");
    // the default is the absence of a pick: nothing kept, no header, as on a hub with one manager
    chooseManager("assistant");
    expect(localStorage.getItem(CHOICE_KEY)).toBeNull();
    expect(managerHeaderFor("/assistant")).toBeUndefined();
  });

  it("is there again after a reload, before the roster is read", async () => {
    localStorage.setItem(CHOICE_KEY, "m-1");
    vi.resetModules();
    const fresh = await import("./manager-choice");
    expect(fresh.chosenManager()).toBe("m-1");
    expect(fresh.managerHeaderFor("/assistant")).toBe("m-1");
    fresh.resetManagerChoice();
  });

  it("goes back to the default when its manager left the roster, or is not installed", () => {
    setRoster(ROSTER);
    chooseManager("m-1");
    setRoster({ ...ROSTER, managers: ROSTER.managers.filter((m) => m.id !== "m-1") });
    expect(chosenManager()).toBeNull();
    expect(shownManager()).toBe("assistant");
    expect(localStorage.getItem(CHOICE_KEY)).toBeNull();
    chooseManager("m-3"); // still installing: nothing to show
    setRoster(ROSTER);
    expect(chosenManager()).toBeNull();
  });

  it("becomes no pick when its manager is made the default", () => {
    setRoster(ROSTER);
    chooseManager("m-1");
    setRoster({ ...ROSTER, default: "m-1" });
    expect(chosenManager()).toBeNull();
    expect(shownManager()).toBe("m-1");
    expect(localStorage.getItem(CHOICE_KEY)).toBeNull();
  });

  it("names the current assistant on its knowledge base and on what it makes, never the health space", () => {
    setRoster(ROSTER);
    expect(currentHeaders()).toBeUndefined();
    chooseManager("m-1");
    for (const path of ["/kb/projects", "/kb-search?q=x", "/recordings/r1/minutes", "/assistant/tasks"]) expect(managerHeaderFor(path)).toBe("m-1");
    expect(currentHeaders()).toEqual({ "X-Persona-Manager": "m-1" });
    // the merged lists stay whole (the pages filter them), and the health space stays the default's
    for (const path of ["/inbox?sources=all", "/todos", "/machines", "/assistant?space=health", "/kbx"]) expect(managerHeaderFor(path)).toBeUndefined();
  });
});

describe("what the pages list", () => {
  it("is everything with one assistant", () => {
    const scope = scopeOf(ONE, "assistant");
    const items = [{ manager: "assistant" }, { manager: undefined }, { manager: "gone" }];
    expect(scope.several).toBe(false);
    expect(scoped(items, scope)).toBe(items);
  });

  it("is the current assistant's with several: no stamp or an unknown one is the default's", () => {
    const items = [{ id: 1, manager: "assistant" }, { id: 2, manager: undefined }, { id: 3, manager: "m-1" }, { id: 4, manager: "gone" }];
    expect(scoped(items, scopeOf(ROSTER, "assistant")).map((x) => x.id)).toEqual([1, 2, 4]);
    expect(scoped(items, scopeOf(ROSTER, "m-1")).map((x) => x.id)).toEqual([3]);
    expect(scopeOf(ROSTER, "m-1").manager?.name).toBe("工作");
  });
});

describe("what waits at each assistant", () => {
  const sessions = [
    session("a", "assistant"),
    session("b", "m-1"),
    session("c", "m-1", { state: "working" }),
    session("quiet", "m-1", { quiet: true }), // a quiet task's agent: the main conversation tells about it
    session("main", "m-1", { assistant: true }),
    session("d", undefined), // no stamp: the default's
  ];

  it("counts an agent once, by a card or by its state, under its manager", () => {
    const cards = [card("x1", "m-1", "b"), card("x2", "m-1", "c"), card("x3", "m-1", "c"), card("x4", "assistant", "")];
    // m-1: b (needs you, and a card: once) and c (two cards: once); the default: a, d and a card without a session
    expect(waitingAgents(ROSTER, sessions, cards)).toEqual({ assistant: 3, "m-1": 2 });
  });

  it("adds the roster's inbox count, none where the hub gives none", () => {
    expect(managerBadges(ROSTER, sessions, [])).toEqual({ assistant: 2 + 4, "m-1": 1 + 2, "m-2": 0 });
    const old = { ...ROSTER, managers: ROSTER.managers.map(({ inbox_needs_you: _n, ...m }) => m) };
    expect(managerBadges(old, sessions, [])).toEqual({ assistant: 2, "m-1": 1, "m-2": 0 });
  });
});

describe("an assistant in the switcher", () => {
  it("has the AI purple when it is the default, else the hue its id's bytes add up to, of five", () => {
    expect(avatarColor("anything", true)).toBe("var(--ai)");
    // "a" is 97, 97 % 5 = 2: orange; "b" 98 % 5 = 3: pink; "c" 4: indigo; "d" 0: blue; "e" 1: green
    expect(["a", "b", "c", "d", "e"].map((id) => avatarColor(id, false))).toEqual(["var(--hue-orange)", "var(--hue-pink)", "var(--hue-indigo)", "var(--hue-blue)", "var(--hue-green)"]);
    // UTF-8 bytes, not UTF-16 units: "é" is 0xC3 0xA9 = 364, 364 % 5 = 4
    expect(avatarColor("é", false)).toBe("var(--hue-indigo)");
  });

  it("shows its name's first character, whole and upper case", () => {
    expect(avatarInitial("工作")).toBe("工");
    expect(avatarInitial(" work")).toBe("W");
    expect(avatarInitial("👩‍💻 dev")).toBe("👩‍💻");
    expect(avatarInitial("")).toBe("");
  });

  it("says where an online one runs, when an offline one last synced, and how long an install code lasts", () => {
    expect(switcherLine(ROSTER.managers[1])).toBe("在线 · MacBook");
    expect(switcherLine(ROSTER.managers[2])).toContain("离线");
    expect(switcherLine(ROSTER.managers[3])).toBe("安装中 · 码 8 分钟后过期");
  });
});

describe("the page after a switch", () => {
  it("leaves one thing of the assistant left behind for its list; a list stays", () => {
    expect(routeAfterSwitch({ name: "persona" })).toBeNull();
    expect(routeAfterSwitch({ name: "persona", sid: "c1" })).toEqual({ name: "persona" });
    expect(routeAfterSwitch({ name: "session", mid: "mac", sid: "s1" })).toEqual({ name: "persona" });
    expect(routeAfterSwitch({ name: "machine", mid: "mac" })).toEqual({ name: "machines" });
    expect(routeAfterSwitch({ name: "inbox", id: "i1", manager: "m-1" })).toEqual({ name: "inbox" });
    expect(routeAfterSwitch({ name: "inbox" })).toBeNull();
    expect(routeAfterSwitch({ name: "kb", kind: "people", id: "p1" })).toEqual({ name: "kb", kind: "people" });
    expect(routeAfterSwitch({ name: "todos", id: "t1", view: "calendar", mode: "week" })).toEqual({ name: "todos", view: "calendar", mode: "week" });
    for (const name of ["settings", "machines", "routines", "home", "health", "agent-list"] as const) expect(routeAfterSwitch({ name })).toBeNull();
  });
});
