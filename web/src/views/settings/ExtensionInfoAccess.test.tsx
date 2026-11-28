// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import { chooseManager, MANAGER_HEADER, resetManagerChoice } from "../../lib/manager-choice";
import { resetRoster, setRoster } from "../../lib/managers";
import { ExtensionInfoAccess } from "./ExtensionInfoAccess";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const managers = [
  { id: "assistant", name: "Home", online: true },
  { id: "work", name: "Work", online: true },
];
let host: HTMLDivElement;
let root: Root;
let rosterAvailable: boolean;
const calls: { method: string; manager: string | null }[] = [];

beforeEach(async () => {
  await initI18n("en");
  setRoster({ default: "assistant", managers });
  rosterAvailable = true;
  calls.length = 0;
  const json = (value: unknown) => new Response(JSON.stringify(value));
  let items: unknown[] = [];
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    if (url.endsWith("/managers")) return rosterAvailable
      ? json({ default: "assistant", managers }) : Response.json({ error: "Missing route" }, { status: 404 });
    if (!url.includes("/assistant/extension-trust")) return json({});
    const method = init?.method ?? "GET";
    calls.push({ method, manager: new Headers(init?.headers).get(MANAGER_HEADER) });
    if (method !== "PUT") return json({ items });
    const accepted = { extension: "notes", trust: "owner", categories: [] };
    items = [accepted];
    return json(accepted);
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  resetManagerChoice();
  resetRoster();
  vi.unstubAllGlobals();
});

it("reads the default-owned card and saves to the opening owner after the default changes", async () => {
  chooseManager("work");
  await act(async () => root.render(<ExtensionInfoAccess extension="notes" />));
  expect(calls).toContainEqual({ method: "GET", manager: "assistant" });

  await act(async () => setRoster({ default: "work", managers }));
  await act(async () => host.querySelector<HTMLButtonElement>('[role="switch"]')!.click());
  expect(calls).toContainEqual({ method: "PUT", manager: "assistant" });
  expect(calls.every(({ manager }) => manager === "assistant")).toBe(true);
});

it("uses headerless reads and writes on a hub from before the roster", async () => {
  resetRoster();
  rosterAvailable = false;
  await act(async () => root.render(<ExtensionInfoAccess extension="notes" />));
  await act(async () => host.querySelector<HTMLButtonElement>('[role="switch"]')!.click());
  expect(calls).toEqual([{ method: "GET", manager: null }, { method: "PUT", manager: null }, { method: "GET", manager: null }]);
});
