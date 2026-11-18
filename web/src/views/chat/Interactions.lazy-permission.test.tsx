// @vitest-environment jsdom
// A permission card's own chunk can be slow or unavailable after a deploy: its slot keeps saying what is waiting, and
// a local failure offers a retry without taking down the surrounding conversation.
import { act, Suspense, type ComponentType } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
const reported = vi.hoisted(() => vi.fn());
vi.mock("../../lib/error-reports", () => ({ reportBoundary: reported }));
import { PermissionCardBoundary, PermissionCardLoading } from "./Interactions";
import { lazyPage, type LazyPageComponent } from "../../lib/lazy-page";
import { t } from "../../i18n";
import type { Interaction } from "../../lib/types";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ix = { id: "p_lazy", machine_id: "m", session_id: "s", kind: "permission", tool_name: "Bash", tool_input: { command: "npm test" } } as Interaction;
type CardComponent = ComponentType<{ ix: Interaction }>;
let host: HTMLDivElement;
let root: Root;

async function render(Lazy: LazyPageComponent<{ ix: Interaction }>) {
  await act(async () => {
    root.render(
      <PermissionCardBoundary ix={ix}>
        <Suspense fallback={<PermissionCardLoading ix={ix} />}><Lazy ix={ix} /></Suspense>
      </PermissionCardBoundary>,
    );
    await Promise.resolve();
  });
}

beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reported.mockClear();
});

describe("a lazy permission card", () => {
  it("keeps an identifiable loading slot until its controls arrive", async () => {
    let arrive!: (card: CardComponent) => void;
    const Lazy = lazyPage(() => new Promise<CardComponent>((resolve) => { arrive = resolve; }));
    await render(Lazy);
    await vi.waitFor(() => expect(host.querySelector('[role="status"]')?.textContent).toBe(t("chat-interactions.permission_loading")));
    expect(host.querySelector("[data-pending-card]")?.getAttribute("aria-label")).toBe(t("chat-interactions.permission_title", { tool: "Bash" }));
    const Loaded = () => <button type="button">Loaded approval</button>;
    await act(async () => arrive(Loaded));
    await vi.waitFor(() => expect(host.textContent).toContain("Loaded approval"));
    expect(host.querySelector('[role="status"]')).toBeNull();
  });

  it("contains a failed import and retries it locally", async () => {
    const failure = new TypeError("Failed to fetch dynamically imported module");
    const Loaded = () => <button type="button">Recovered approval</button>;
    const load = vi.fn<() => Promise<CardComponent>>().mockRejectedValueOnce(failure).mockResolvedValue(Loaded);
    const Lazy = lazyPage(load);
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      await render(Lazy);
      await vi.waitFor(() => expect(host.querySelector('[role="alert"]')?.textContent).toContain(t("chat-interactions.permission_load_failed")), { timeout: 20_000 });
      expect(host.textContent).toContain(t("chat-interactions.permission_load_hint"));
      expect(host.textContent).not.toContain("Recovered approval");
      expect(reported).toHaveBeenCalledWith(failure, "permission_card", true, expect.anything());
      const retry = [...host.querySelectorAll("button")].find((b) => b.textContent === t("chat-interactions.permission_load_retry"))!;
      act(() => retry.click());
      await vi.waitFor(() => expect(host.textContent).toContain("Recovered approval"), { timeout: 20_000 });
      expect(load).toHaveBeenCalledTimes(2);
      expect(host.querySelector('[role="alert"]')).toBeNull();
    } finally {
      consoleError.mockRestore();
    }
  });
});
