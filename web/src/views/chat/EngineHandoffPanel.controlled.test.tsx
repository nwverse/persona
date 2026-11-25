// @vitest-environment jsdom
// Controlled handoff panels own full-text loading without touching useFullText or lib/api; default fallbacks stay pinned too.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { EngineSwitch } from "../../lib/engine-switch";
import type { Item } from "../../lib/types";
import type { EngineHandoffFullItem, EngineHandoffPanelProps } from "./EngineHandoffPanel";

const mocks = vi.hoisted(() => ({
  fullItem: vi.fn(),
  useFullText: vi.fn(),
}));

vi.mock("../../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../lib/api")>()),
  fullItem: mocks.fullItem,
}));

vi.mock("./FullText", async (importOriginal) => {
  const original = await importOriginal<typeof import("./FullText")>();
  mocks.useFullText.mockImplementation((item: Item | undefined, mid: string | undefined, sid: string | undefined) =>
    original.useFullText(item, mid, sid),
  );
  return {
    ...original,
    useFullText: mocks.useFullText,
  };
});

const { EngineHandoffPanel } = await import("./EngineHandoffPanel");
const { t } = await import("../../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const HANDOFF = "## Conversation so far\nThe user asked for a login page.";
const CUT_MARK = "\n…（已截断，共 9000 字节）"; // i18n-ignore: matches the hub's cut note
const block = (text: string) => `<persona-context kind="engine-switch" title="Handoff" from="claude" to="codex">\n${text}\n</persona-context>\n\n`;
const makeSwitch = (text = HANDOFF): EngineSwitch => ({ from: "claude", to: "codex", model: "gpt-5.5", resumed: false, text });
const makeCutItem = (extra: Partial<Item> = {}): Item => ({
  id: "item-1",
  seq: 1,
  role: "user",
  kind: "text",
  text: `${block(`${HANDOFF}${CUT_MARK}`)}Continue.`,
  full_size: 9000,
  ...extra,
});

let host: HTMLDivElement;
let root: Root;

const mount = async (props: Partial<EngineHandoffPanelProps> = {}) => {
  await act(async () =>
    root.render(
      <EngineHandoffPanel
        id="handoff-panel-1"
        sw={makeSwitch()}
        {...props}
      />,
    ),
  );
};
const panel = () => host.querySelector<HTMLElement>("[data-engine-handoff]");
const preText = () => panel()?.querySelector("pre")?.textContent;
const fullButton = () => host.querySelector<HTMLButtonElement>("[data-full-text]");
const flush = async () => {
  for (let i = 0; i < 4; i++) await act(async () => { await Promise.resolve(); });
};

beforeEach(() => {
  mocks.fullItem.mockReset();
  mocks.useFullText.mockClear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("EngineHandoffPanel with controlled props", () => {
  it("renders explicit fullText directly without invoking useFullText or fullItem", async () => {
    const complete = `${HANDOFF}\nComplete controlled handoff.`;
    await mount({
      sw: makeSwitch(`${HANDOFF}${CUT_MARK}`),
      item: makeCutItem(),
      fullText: complete,
    });

    expect(panel()?.id).toBe("handoff-panel-1");
    expect(panel()?.textContent).toContain(t("chat-engine.handoff_title"));
    expect(preText()).toBe(complete);
    expect(fullButton()).toBeNull();
    expect(mocks.useFullText).not.toHaveBeenCalled();
    expect(mocks.fullItem).not.toHaveBeenCalled();
  });

  it("renders a custom footer control or null without touching default full-text hooks", async () => {
    await mount({
      sw: makeSwitch(`${HANDOFF}${CUT_MARK}`),
      item: makeCutItem(),
      more: <button type="button" data-custom-more>Custom load</button>,
    });

    expect(preText()).toBe(HANDOFF);
    expect(host.querySelector("[data-custom-more]")?.textContent).toBe("Custom load");
    expect(fullButton()).toBeNull();
    expect(mocks.useFullText).not.toHaveBeenCalled();
    expect(mocks.fullItem).not.toHaveBeenCalled();

    await mount({
      sw: makeSwitch(`${HANDOFF}${CUT_MARK}`),
      item: makeCutItem(),
      more: null,
    });
    expect(preText()).toBe(HANDOFF);
    expect(host.querySelector("[data-custom-more]")).toBeNull();
    expect(fullButton()).toBeNull();
    expect(mocks.useFullText).not.toHaveBeenCalled();
  });

  it("loads a truncated item through loadFullItem, deduplicates clicks and extracts the engine-switch block", async () => {
    let finish!: (value: EngineHandoffFullItem) => void;
    const loadFullItem = vi.fn(() => new Promise<EngineHandoffFullItem>((resolve) => { finish = resolve; }));
    const cut = makeCutItem({ source_fingerprint: "fp-1" });
    await mount({
      sw: makeSwitch(`${HANDOFF}${CUT_MARK}`),
      item: cut,
      loadFullItem,
    });

    expect(preText()).toBe(HANDOFF);
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");
    expect(mocks.useFullText).not.toHaveBeenCalled();

    act(() => fullButton()!.click());
    expect(loadFullItem).toHaveBeenCalledTimes(1);
    expect(loadFullItem).toHaveBeenCalledWith(cut);
    expect(fullButton()?.getAttribute("data-full-text")).toBe("loading");
    expect(fullButton()?.disabled).toBe(true);
    expect(fullButton()?.getAttribute("aria-busy")).toBe("true");

    act(() => fullButton()!.click());
    expect(loadFullItem).toHaveBeenCalledTimes(1);

    const whole = `${HANDOFF}\nAnd the rest of the handoff.`;
    await act(async () => {
      finish({ text: `${block(whole)}Continue.`, source_fingerprint: "fp-1" });
      await Promise.resolve();
    });
    await flush();

    expect(preText()).toBe(whole);
    expect(fullButton()).toBeNull();
    expect(mocks.useFullText).not.toHaveBeenCalled();
    expect(mocks.fullItem).not.toHaveBeenCalled();
  });

  it("handles controlled load failures and fingerprint mismatches, then retries with a plain string result", async () => {
    const onLoadError = vi.fn();
    const loadFullItem = vi
      .fn<(item: Item) => Promise<EngineHandoffFullItem>>()
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce({ text: `${block("stale")}`, source_fingerprint: "wrong-fp" })
      .mockResolvedValueOnce(`${HANDOFF}\nRecovered plain handoff.`);
    const cut = makeCutItem({ source_fingerprint: "fp-2" });

    await mount({
      sw: makeSwitch(`${HANDOFF}${CUT_MARK}`),
      item: cut,
      loadFullItem,
      onLoadError,
    });

    act(() => fullButton()!.click());
    await flush();
    expect(fullButton()?.getAttribute("data-full-text")).toBe("failed");
    expect(preText()).toBe(HANDOFF);
    expect(onLoadError).toHaveBeenCalledTimes(1);

    act(() => fullButton()!.click());
    await flush();
    expect(fullButton()?.getAttribute("data-full-text")).toBe("failed");
    expect(preText()).toBe(HANDOFF);
    expect(onLoadError).toHaveBeenCalledTimes(2);

    act(() => fullButton()!.click());
    await flush();
    expect(preText()).toBe(`${HANDOFF}\nRecovered plain handoff.`);
    expect(fullButton()).toBeNull();
    expect(mocks.useFullText).not.toHaveBeenCalled();
    expect(mocks.fullItem).not.toHaveBeenCalled();
  });

  it("ignores stale loadFullItem results when the truncated item changes mid-request", async () => {
    let finishFirst!: (value: EngineHandoffFullItem) => void;
    const loadFullItem = vi.fn(() => new Promise<EngineHandoffFullItem>((resolve) => { finishFirst = resolve; }));
    const first = makeCutItem({ id: "item-1" });
    const second = makeCutItem({ id: "item-2" });

    await mount({
      sw: makeSwitch(`${HANDOFF}${CUT_MARK}`),
      item: first,
      loadFullItem,
    });
    act(() => fullButton()!.click());
    expect(fullButton()?.getAttribute("data-full-text")).toBe("loading");

    await mount({
      sw: makeSwitch(`Second preview${CUT_MARK}`),
      item: second,
      loadFullItem,
    });
    expect(preText()).toBe("Second preview");
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");

    await act(async () => {
      finishFirst("Stale first full handoff");
      await Promise.resolve();
    });
    await flush();

    expect(preText()).toBe("Second preview");
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");
  });

  it("renders non-truncated handoff verbatim without calling loadFullItem or useFullText", async () => {
    const loadFullItem = vi.fn();
    await mount({ sw: makeSwitch(HANDOFF), loadFullItem });
    expect(preText()).toBe(HANDOFF);
    expect(fullButton()).toBeNull();
    expect(loadFullItem).not.toHaveBeenCalled();
    expect(mocks.useFullText).not.toHaveBeenCalled();
    expect(mocks.fullItem).not.toHaveBeenCalled();
  });
});

describe("EngineHandoffPanel default compatibility", () => {
  it("uses useFullText and lib/api fullItem when controlled props are absent", async () => {
    const whole = `${HANDOFF}\nLoaded from default API.`;
    mocks.fullItem.mockResolvedValue({
      id: "item-1",
      seq: 1,
      role: "user",
      kind: "text",
      text: `${block(whole)}Continue.`,
    });

    await mount({
      sw: makeSwitch(`${HANDOFF}${CUT_MARK}`),
      item: makeCutItem(),
      mid: "example-machine",
      sid: "example-session",
    });

    expect(mocks.useFullText).toHaveBeenCalled();
    expect(preText()).toBe(HANDOFF);
    expect(fullButton()?.getAttribute("data-full-text")).toBe("idle");

    act(() => fullButton()!.click());
    await flush();

    expect(mocks.fullItem).toHaveBeenCalledWith("example-machine", "example-session", "item-1");
    expect(preText()).toBe(whole);
    expect(fullButton()).toBeNull();
  });

  it("keeps plain rendering for non-truncated items without calling fullItem", async () => {
    await mount({
      sw: makeSwitch(HANDOFF),
      mid: "example-machine",
      sid: "example-session",
    });

    expect(mocks.useFullText).toHaveBeenCalled();
    expect(preText()).toBe(HANDOFF);
    expect(fullButton()).toBeNull();
    expect(mocks.fullItem).not.toHaveBeenCalled();
  });
});
