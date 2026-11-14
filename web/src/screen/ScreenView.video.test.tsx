// @vitest-environment jsdom
// The live screen's picture switcher (#3954, #6558): the bar offers Video / VNC — but only when this Mac's node
// advertises the hardware video channel and this browser decodes it, so no Mac shows a control it can't use — and, for
// video, a quality bias once the helper and this page both have quality tiers. The choice is kept per Mac in this
// browser. A page an app embeds has no bar to choose in.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../i18n";
import { webTiers, webVideo } from "./video";

type FakeRFB = EventTarget & { canvas: HTMLCanvasElement };
const rfbs = vi.hoisted(() => [] as FakeRFB[]);
const status = vi.hoisted(() => ({ video: null as unknown }));
// The video chunk (#6558): what the browser decodes, and whether the helper said it has tiers.
const chunk = vi.hoisted(() => ({ probe: { codecs: ["hevc", "h264"] } as { codecs: string[]; why?: string }, tiers: false }));
vi.mock("./VideoPicture", async () => {
  const { useEffect } = await import("react");
  return {
    probeHere: async () => chunk.probe,
    VideoChannel: ({ onState }: { onState: (s: unknown) => void }) => {
      useEffect(() => onState({ showing: false, fallback: null, reading: {}, codec: null, size: null, tiers: chunk.tiers, decoderFailed: false }), [onState]);
      return null;
    },
    FallbackLine: () => null,
  };
});
vi.mock("@novnc/novnc", () => ({
  default: class extends EventTarget {
    viewOnly = true;
    canvas = document.createElement("canvas");
    constructor(target: HTMLElement) {
      super();
      target.append(this.canvas);
      rfbs.push(this as unknown as FakeRFB);
    }
    sendCredentials() {}
    disconnect() {
      this.canvas.remove();
    }
  },
}));
vi.mock("../views/theme", () => ({}));
vi.mock("./aes", () => ({ ensureSubtleAES: () => {} }));
vi.mock("../lib/api", async (actual) => ({
  ...(await actual<typeof import("../lib/api")>()),
  api: async (path: string) =>
    path === "/machines"
      ? { machines: [{ id: "macbook", name: "MacBook", online: true }] }
      : { enabled: true, screen_sharing: true, displays: [], screen_video: status.video },
}));
import { ScreenView } from "./ScreenView";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};
const open = async (embedded = false) => {
  act(() => root.render(<ScreenView target={{ mid: "macbook", token: "tok", embedded, name: "MacBook", keep: "", unlock: false }} />));
  await flush();
};
// live; the picture menu, a lazy component, appears a moment later
const connect = async () => {
  act(() => void rfbs.at(-1)!.dispatchEvent(new CustomEvent("connect", { detail: {} })));
  await flush();
};
const button = () => host.querySelector<HTMLButtonElement>('[data-testid="screen-video"]');
const rows = () => [...document.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]')];
const menuAppears = () => vi.waitFor(async () => {
  await act(async () => {});
  expect(button()).not.toBeNull();
}, { timeout: 3000 });

// Preload the chunk; each mounted page still commits its own import callback.
// Positive cases therefore wait for the actual button rather than another fixed flush.
beforeAll(async () => {
  await import("./ScreenMenu");
});

beforeEach(() => {
  rfbs.length = 0;
  status.video = { version: 1, codecs: ["hevc", "h264"] };
  webVideo.plays = true; // the switcher as it will be once the browser plays video (not yet: see the last test)
  chunk.probe = { codecs: ["hevc", "h264"] };
  chunk.tiers = false;
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  webVideo.plays = false;
  webTiers.supported = false;
  act(() => root.unmount());
  host.remove();
  localStorage.clear();
  vi.restoreAllMocks();
});

describe("the picture switcher when the Mac advertises video", () => {
  it("offers Video / VNC and keeps the choice per Mac; no Quality until both sides have tiers", async () => {
    await open();
    await connect();
    await menuAppears();
    // Video by default where it plays, as on the iPhone, until a choice is kept.
    expect(button()?.getAttribute("aria-label")).toBe(t("screen-video.picture_current", { current: t("screen-video.picture_video") }));

    act(() => button()!.click());
    expect(rows().map((r) => [r.textContent, r.getAttribute("aria-checked")])).toEqual([
      [t("screen-video.picture_video"), "true"],
      [t("screen-video.picture_vnc"), "false"],
    ]);

    act(() => rows()[1].click()); // choose VNC
    await flush();
    expect(localStorage.getItem("persona.screen.picture.macbook")).toBe("vnc");
    expect(button()?.getAttribute("aria-label")).toBe(t("screen-video.picture_current", { current: t("screen-video.picture_vnc") }));

    act(() => button()!.click());
    act(() => rows()[0].click()); // and Video again
    await flush();
    expect(localStorage.getItem("persona.screen.picture.macbook")).toBe("video");

    act(() => button()!.click());
    expect(rows().map((r) => r.getAttribute("aria-checked")), "Video checked, and no Quality: a choice that would do nothing").toEqual([
      "true", // Video
      "false", // VNC
    ]);
  });

  it("adds Quality for video once the helper and this page both have tiers", async () => {
    webTiers.supported = true;
    chunk.tiers = true;
    localStorage.setItem("persona.screen.picture.macbook", "video");
    await open();
    await connect();
    await menuAppears();
    act(() => button()!.click());
    expect(rows().map((r) => r.getAttribute("aria-checked")), "Video checked, the quality bias defaults to Auto").toEqual([
      "true", // Video
      "false", // VNC
      "true", // Auto
      "false", // Sharper
      "false", // Smoother
    ]);
    act(() => rows()[3].click()); // Sharper
    await flush();
    expect(localStorage.getItem("persona.screen.quality.macbook")).toBe("sharper");
  });

  it("comes back on the kept picture on the next page", async () => {
    localStorage.setItem("persona.screen.picture.macbook", "video");
    await open();
    await connect();
    await menuAppears();
    expect(button()?.getAttribute("aria-label")).toBe(t("screen-video.picture_current", { current: t("screen-video.picture_video") }));
    act(() => button()!.click());
    expect(rows(), "Video and VNC").toHaveLength(2);
  });
});

describe("the picture switcher otherwise", () => {
  it("is not offered when this browser decodes neither codec (a page over plain http, say)", async () => {
    chunk.probe = { codecs: [], why: "insecure" };
    await open();
    await connect();
    await flush();
    expect(button()).toBeNull();
  });

  it("is not offered when the Mac does not advertise the video channel", async () => {
    status.video = null; // an older node, or no helper
    await open();
    await connect();
    expect(button(), "no control a Mac can't use").toBeNull();
  });

  it("is not offered for version 0", async () => {
    status.video = { version: 0 };
    await open();
    await connect();
    expect(button()).toBeNull();
  });

  it("is not offered on a page an app embeds", async () => {
    await open(true);
    await connect();
    expect(button()).toBeNull();
  });

  it("is not offered while this browser can't play video, even for a Mac that advertises it", async () => {
    webVideo.plays = false; // as shipped until the web's decoder lands
    await open();
    await connect();
    expect(button(), "the web keeps RFB and shows no Video it can't play").toBeNull();
  });
});
