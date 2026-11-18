// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { HiddenAgentDirs, hiddenPatternError, type HiddenAgentDirsState } from "./HiddenAgentDirs";
import { t } from "../../i18n";

const calls: { path: string; method: string; json?: HiddenAgentDirsState; resolve: (v: unknown) => void; reject: (e: Error) => void }[] = [];
vi.mock("../../lib/api", async (orig) => ({ ...await orig<typeof import("../../lib/api")>(), api: (path: string, options?: { method: string; json: HiddenAgentDirsState }) => new Promise((resolve, reject) => calls.push({ path, method: options?.method ?? "GET", json: options?.json, resolve, reject })) }));
vi.mock("../../lib/current-assistant", () => ({ useScopedMachines: () => [{ id: "computer", name: "Computer", online: false }, { id: "other", name: "Other", online: true }] }));
let host: HTMLDivElement, root: Root;
beforeEach(() => {
 (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
 calls.length = 0; host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); });
const original: HiddenAgentDirsState = { rules: [{ pattern: "~/work/*", enabled: true }, { pattern: "*scratch*", enabled: false }], home: "/home/me" };
async function show() {
 await act(async () => root.render(<HiddenAgentDirs />));
 await act(async () => calls[0].resolve(original));
}
it("loads an offline computer's own list and toggles one rule by saving that computer's whole list", async () => {
 await show();
 expect(calls[0].path).toBe("/machines/computer/agent-dirs/hidden");
 const sw = host.querySelector<HTMLButtonElement>('[role="switch"]')!;
 expect(sw.getAttribute("aria-checked")).toBe("true");
 await act(async () => sw.click());
 expect(sw.getAttribute("aria-checked")).toBe("false");
 expect(sw.disabled).toBe(true);
 expect(calls[1]).toMatchObject({ path: "/machines/computer/agent-dirs/hidden", method: "PUT", json: { rules: [{ ...original.rules[0], enabled: false }, original.rules[1]] } });
 await act(async () => calls[1].resolve(calls[1].json));
 expect(sw.disabled).toBe(false);
});
it("switching computers loads the other computer's list", async () => {
 await show();
 const select = host.querySelector<HTMLSelectElement>("select")!;
 await act(async () => { select.value = "other"; select.dispatchEvent(new Event("change", { bubbles: true })); });
 expect(calls.at(-1)!.path).toBe("/machines/other/agent-dirs/hidden");
 await act(async () => calls.at(-1)!.resolve({ rules: [] }));
 expect(host.textContent).toContain(t("hidden-agent-dirs.empty"));
});
it("rolls back rejected changes and displays a retryable failure", async () => {
 await show();
 await act(async () => host.querySelector<HTMLButtonElement>('[role="switch"]')!.click());
 await act(async () => calls[1].reject(new Error("unavailable")));
 expect(host.querySelector('[role="switch"]')!.getAttribute("aria-checked")).toBe("true");
 expect(host.textContent).toContain("unavailable");
 expect(host.textContent).toContain(t("hidden-agent-dirs.retry"));
});
it("says why a typed folder cannot be added", () => {
 expect(hiddenPatternError("relative", [])).toBe(t("hidden-agent-dirs.pattern_prefix"));
 expect(hiddenPatternError("~/", [])).toBe(t("hidden-agent-dirs.pattern_root"));
 expect(hiddenPatternError("~/work/*/", original.rules)).toBe(t("hidden-agent-dirs.pattern_exists"));
 expect(hiddenPatternError("*multiremi*", original.rules)).toBe("");
});
