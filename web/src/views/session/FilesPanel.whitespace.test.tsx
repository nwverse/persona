// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.mock("../../lib/use-fs-live", () => ({ useFsLive: () => {} }));
import { setToken } from "../../lib/api";
import { getState } from "../../lib/store";
import { t } from "../../i18n";
import { FilesPanel } from "./FilesPanel";
import { displayFileName, spokenFileName } from "./files/FileName";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
window.matchMedia = ((media: string) => ({ matches: media.includes("max-width"), media, addEventListener() {}, removeEventListener() {} })) as unknown as typeof window.matchMedia;
HTMLElement.prototype.scrollTo ??= () => {};
let host: HTMLDivElement, root: Root, calls: { path: string; body: unknown }[];
const oldFeatures = getState().features;
const flush = async () => { for (let i = 0; i < 12; i++) await act(async () => { await Promise.resolve(); }); };
const names = ["report", "report ", "report\u00a0", " leading", " "];
const clickButton = async (label: string) => {
  const button = [...document.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label || b.getAttribute("aria-label") === label);
  expect(button, label).toBeTruthy();
  await act(async () => button!.click());
  await flush();
};

beforeEach(() => {
  localStorage.clear(); setToken("synthetic-credential"); Object.assign(getState(), { features: ["file_edit"] }); calls = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const u = new URL(String(input), "http://local.invalid");
    if (u.pathname.endsWith("/git/status")) return Response.json({ is_repo: false, files: [], commits: [] });
    if (u.pathname.endsWith("/fs")) {
      const path = u.searchParams.get("path");
      const entries = path === "/work" ? [...names.map((name) => ({ name, dir: false, size: 1 })), { name: "dest", dir: true, size: 0 }, { name: "dest ", dir: true, size: 0 }] : [];
      return Response.json({ path, entries, truncated: false });
    }
    calls.push({ path: u.pathname, body: init?.body ? JSON.parse(String(init.body)) : undefined });
    if (u.pathname.endsWith("/files/trash")) return Response.json({ trashed: [{ path: "/work/report ", trash_id: "synthetic" }] });
    if (u.pathname.endsWith("/files/move")) return Response.json({ moved: [{ from: "/work/report ", to: "/work/dest /report " }] });
    return Response.json({});
  }));
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); document.body.innerHTML = ""; Object.assign(getState(), { features: oldFeatures }); setToken(""); vi.unstubAllGlobals(); });

describe("file-list whitespace identity", () => {
  it.each(["tree", "drill"])("marks and announces each name in %s view while actions keep its original path", async (mode) => {
    localStorage.setItem("persona.files.view", mode);
    await act(async () => root.render(<FilesPanel mid="synthetic-machine" sid="synthetic-session" cwd="/work" onClose={() => {}} />));
    await flush();
    for (const name of names) {
      const row = [...host.querySelectorAll<HTMLButtonElement>("button[title]")].find((b) => b.title === `/work/${name}`);
      expect(row, JSON.stringify(name)).toBeTruthy();
      if (name !== "report") {
        expect([...row!.querySelectorAll('[aria-hidden="true"]')].map((e) => e.textContent).join("")).toContain(displayFileName(name));
        expect(row!.querySelector(".sr-only")?.textContent).toBe(spokenFileName(name));
      } else {
        expect(row!.textContent).toContain("report");
      }
      const more = [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.getAttribute("aria-label") === t("files-ops.row_more", { name: spokenFileName(name) }));
      expect(more, `menu for ${JSON.stringify(name)}`).toBeTruthy();
    }
    await clickButton(t("session-filespanel.more"));
    await clickButton(t("files-ops.select"));
    const select = [...host.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')].find((e) => e.getAttribute("aria-label") === t("files-ops.select_row", { name: spokenFileName("report ") }));
    expect(select).toBeTruthy();
    await act(async () => select!.click()); await flush();
    const toolbar = host.querySelector('[role="toolbar"]')!;
    const trash = toolbar.querySelector<HTMLButtonElement>(`button[aria-label="${t("files-ops.trash")}"]`)!;
    await act(async () => trash.click()); await flush();
    const heading = document.querySelector<HTMLElement>('[role="dialog"] h2')!;
    expect(heading.querySelector('[aria-hidden="true"]')?.textContent).toBe(t("files-ops.trash_one_title", { name: displayFileName("report ") }));
    expect(heading.querySelector(".sr-only")?.textContent).toBe(t("files-ops.trash_one_title", { name: spokenFileName("report ") }));
    const confirm = [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find((b) => b.textContent?.trim() === t("files-ops.trash"));
    expect(confirm).toBeTruthy();
    await act(async () => confirm!.click()); await flush();
    expect(calls.find((call) => call.path.endsWith("/files/trash"))?.body).toEqual({ paths: ["/work/report "], cwd: "/work" });
  });

  it("marks a move destination and sends the untouched directory name", async () => {
    localStorage.setItem("persona.files.view", "drill");
    await act(async () => root.render(<FilesPanel mid="synthetic-machine" sid="synthetic-session" cwd="/work" onClose={() => {}} />)); await flush();
    await clickButton(t("session-filespanel.more")); await clickButton(t("files-ops.select"));
    const select = [...host.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')].find((e) => e.getAttribute("aria-label") === t("files-ops.select_row", { name: spokenFileName("report ") }))!;
    await act(async () => select.click()); await flush();
    const toolbar = host.querySelector('[role="toolbar"]')!;
    const move = toolbar.querySelector<HTMLButtonElement>(`button[aria-label="${t("files-ops.move")}"]`)!;
    await act(async () => move.click()); await flush();
    const dialog = document.querySelector<HTMLElement>('[role="dialog"]')!;
    expect(dialog.querySelector("h2 .sr-only")?.textContent).toBe(t("files-ops.move_one_title", { name: spokenFileName("report ") }));
    const destination = [...dialog.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.querySelector(".sr-only")?.textContent === spokenFileName("dest "));
    expect(destination).toBeTruthy();
    await act(async () => destination!.click()); await flush();
    await clickButton(t("files-ops.move_here"));
    expect(calls.find((call) => call.path.endsWith("/files/move"))?.body).toEqual({ paths: ["/work/report "], cwd: "/work", dir: "/work/dest " });
  });
});
