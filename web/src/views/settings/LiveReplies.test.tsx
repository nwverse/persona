// @vitest-environment jsdom
// Settings › Claude Code › Live replies (#1309): one switch per computer, on unless turned off. A change goes to the
// shown computer and, with "Change all online machines", to the other online ones; a refused change on the shown
// computer goes back. An older hub without the route shows no switch.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { ApiError } from "../../lib/api";

type Call = { path: string; method: string; json?: { on: boolean }; resolve: (v: unknown) => void; reject: (e: unknown) => void };
const calls: Call[] = [];
vi.mock("../../lib/api", async (orig) => ({
  ...(await orig<typeof import("../../lib/api")>()),
  api: (path: string, init?: { method?: string; json?: { on: boolean } }) =>
    new Promise((resolve, reject) => calls.push({ path, method: init?.method ?? "GET", json: init?.json, resolve, reject })),
}));
const toasts: { text: string; tone: string }[] = [];
vi.mock("../../ui/atoms", async (orig) => ({
  ...(await orig<typeof import("../../ui/atoms")>()),
  toast: (text: string, tone: string) => toasts.push({ text, tone }),
}));

const { LiveReplies } = await import("./LiveReplies");

let root: Root;
let host: HTMLDivElement;
beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  calls.length = 0;
  toasts.length = 0;
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const NAMES: Record<string, string> = { "m-1": "Work", "m-2": "Home" };
const name = (id: string) => NAMES[id] ?? id;
const label = () => t("settings-livereplies.label");
const sw = () => host.querySelector<HTMLButtonElement>(`[role="switch"][aria-label="${label()}"]`);
const get = (mid: string) => calls.find((c) => c.method === "GET" && c.path === `/machines/${mid}/claude-live-replies`);
const puts = () => calls.filter((c) => c.method === "PUT");
const answer = (c: Call | undefined, v: unknown) => act(async () => c!.resolve(v));
const fail = (c: Call | undefined, e: unknown) => act(async () => c!.reject(e));

async function shown(on: boolean, others: string[] = []) {
  await act(async () => root.render(<LiveReplies mid="m-1" name={name} others={others} />));
  await answer(get("m-1"), { on });
}

describe("Settings > Claude Code > Live replies", () => {
  it("shows the computer's setting and says what it costs", async () => {
    await shown(true);
    expect(sw()?.getAttribute("aria-checked")).toBe("true");
    expect(host.textContent).toContain(t("settings-livereplies.title", { name: "Work" }));
    expect(host.textContent).toContain(t("settings-livereplies.footer"));
    expect(host.textContent).toContain(t("settings-livereplies.detail_on"));
  });

  it("lets the line under the switch wrap instead of cutting it", async () => {
    await shown(true);
    const hint = [...host.querySelectorAll("div")].find((d) => d.textContent === t("settings-livereplies.detail_on"));
    expect(hint).toBeTruthy();
    expect(hint!.className).not.toContain("truncate");
  });

  it("turns it off on the shown computer only when Change all is off", async () => {
    await shown(true);
    await act(async () => sw()!.click());
    expect(puts().map((c) => [c.path, c.json])).toEqual([["/machines/m-1/claude-live-replies", { on: false }]]);
    expect(sw()?.disabled).toBe(true); // busy until the hub answers
    await answer(puts()[0], { on: false });
    expect(sw()?.getAttribute("aria-checked")).toBe("false");
    expect(sw()?.disabled).toBe(false);
    expect(host.textContent).toContain(t("settings-livereplies.detail_off"));
    expect(toasts.at(-1)?.tone).toBe("ok");
  });

  it("sends a change to every other online computer with Change all", async () => {
    await shown(false, ["m-2"]);
    await act(async () => sw()!.click());
    expect(puts().map((c) => [c.path, c.json])).toEqual([
      ["/machines/m-1/claude-live-replies", { on: true }],
      ["/machines/m-2/claude-live-replies", { on: true }],
    ]);
    await answer(puts()[0], { on: true });
    await fail(puts()[1], new Error("offline"));
    expect(sw()?.getAttribute("aria-checked")).toBe("true");
    expect(toasts.at(-1)?.tone).toBe("error"); // Home did not take it, and the toast says so
    expect(toasts.at(-1)?.text).toContain("Home");
  });

  it("goes back when the shown computer refuses", async () => {
    await shown(true);
    await act(async () => sw()!.click());
    await fail(puts()[0], new Error("no store"));
    expect(sw()?.getAttribute("aria-checked")).toBe("true");
    expect(toasts.at(-1)?.tone).toBe("error");
  });

  it("shows nothing on an older hub without the route", async () => {
    await act(async () => root.render(<LiveReplies mid="m-1" name={name} others={[]} />));
    await fail(get("m-1"), new ApiError(404, "not found", {}));
    expect(host.textContent).toBe("");
  });

  it("says when it could not read the setting, and reads it again on Retry", async () => {
    await act(async () => root.render(<LiveReplies mid="m-1" name={name} others={[]} />));
    await fail(get("m-1"), new ApiError(500, "boom", {}));
    expect(sw()).toBeNull();
    const retry = [...host.querySelectorAll("button")].find((b) => b.textContent?.includes(t("settings-livereplies.retry")));
    calls.length = 0;
    await act(async () => retry!.click());
    await answer(get("m-1"), { on: true });
    expect(sw()?.getAttribute("aria-checked")).toBe("true");
  });
});
