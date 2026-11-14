// The live screen's video channel on the page (#3954, #6558; docs/extensions/computer-use.md "Live screen video
// (#3954)"): a chunk of its own that ScreenView loads only for a Mac whose node offers video, so neither its code nor
// its words (screen-video) are in the first screen. It probes what this browser decodes, runs the channel
// (./video-link) over the hub's relay, draws each picture on a canvas over noVNC's at the display's place, and says why
// when the page falls back to VNC. Input always stays on RFB: noVNC's canvas stays underneath at opacity 0.
import { useEffect, useRef, useState } from "react";
import { Dialog } from "../ui/atoms";
import { t } from "../i18n";
import { holdUpdates } from "./rfb-hold";
import { ScreenVideoLink, type Fallback, type Reading, type Want } from "./video-link";
import { relayTransport, videoSocket, type DecoderMaker, type Picture } from "./video-stream";
import type { VideoCodec } from "./video-probe";

import { probeCodecs } from "./video-probe";

/** What this browser decodes at the size the page will ask for (`budget`). */
export function probeHere() {
  const b = budget();
  return probeCodecs(b.maxSize, Math.round((b.maxSize * 10) / 16));
}

/** What the page shows of the channel: the picture, why VNC shows instead, and the overlay's numbers. */
export type VideoState = {
  showing: boolean;
  fallback: Fallback | null;
  reading: Reading;
  codec: string | null;
  size: { width: number; height: number } | null;
  tiers: boolean;
  decoderFailed: boolean;
};

/** The cap and frame rate the page asks for: Wi-Fi's (mobile data's rules come with #6558's data-use PR). The long
 *  edge is this screen's, at least 1920 and at most 4K, as the iPhone asks for its own. */
function budget() {
  const long = Math.max(window.screen?.width ?? 1920, window.screen?.height ?? 1080) * (window.devicePixelRatio || 1);
  return { maxKbps: 12_000, fps: 60, maxSize: Math.min(3840, Math.max(1920, Math.round(long))) };
}

/** The video over noVNC's canvas. `screen` is the element noVNC draws in; `pair` the signed-in connection's, null while
 *  RFB isn't; `want` what to show (null: VNC). */
export function VideoChannel({
  rfb,
  screen,
  pair,
  want,
  codecs,
  mid,
  token,
  onState,
  makeDecoder,
}: {
  rfb: object;
  screen: HTMLElement | null;
  pair: string | null;
  want: Want | null;
  codecs: VideoCodec[];
  mid: string;
  token: string;
  onState: (s: VideoState) => void;
  makeDecoder?: DecoderMaker;
}) {
  const canvas = useRef<HTMLCanvasElement>(null);
  const [link, setLink] = useState<ScreenVideoLink | null>(null);
  const stateCb = useRef(onState);
  stateCb.current = onState;

  // One link a page; its inputs follow the props.
  useEffect(() => {
    const b = budget();
    const link = new ScreenVideoLink({
      open: (r) => relayTransport(videoSocket(window.location, mid, token, r.pair)),
      codecs,
      maxSize: b.maxSize,
      maxKbps: b.maxKbps,
      fps: b.fps,
      makeDecoder,
    });
    setLink(link);
    const tell = () =>
      stateCb.current({ showing: link.showing, fallback: link.fallback, reading: link.reading, codec: link.codec, size: link.size, tiers: link.tiers, decoderFailed: link.decoderFailed });
    link.onChange = tell;
    link.onReading = tell;
    const hidden = () => link.hold("background", document.visibilityState === "hidden");
    document.addEventListener("visibilitychange", hidden);
    hidden();
    return () => {
      document.removeEventListener("visibilitychange", hidden);
      link.close();
      setLink(null);
    };
  }, [mid, token, codecs, makeDecoder]);

  useEffect(() => link?.want(want), [link, want]);
  useEffect(() => link?.rfb(pair ? { pair } : null), [link, pair]);

  // Showing: noVNC asks for no updates and its canvas goes clear; the newest picture is drawn at the next frame.
  useEffect(() => {
    if (!link || !screen) return;
    let latest: Picture | null = null;
    let frame = 0;
    const vnc = () => [...screen.querySelectorAll("canvas")].find((c) => c !== canvas.current) ?? null;
    const place = () => {
      const c = canvas.current, v = vnc();
      if (!c || !v || !v.width) return;
      // relative to what the video canvas is placed in (the page's picture area), so it lies on noVNC's picture
      const box = v.getBoundingClientRect(), host = ((c.offsetParent as HTMLElement | null) ?? screen).getBoundingClientRect();
      const scale = box.width / v.width;
      const [x, y, w, h] = link.rect ?? [0, 0, v.width, v.height];
      Object.assign(c.style, { left: `${box.left - host.left + x * scale}px`, top: `${box.top - host.top + y * scale}px`, width: `${w * scale}px`, height: `${h * scale}px` });
    };
    const draw = () => {
      frame = 0;
      const c = canvas.current, p = latest;
      latest = null;
      if (!c || !p) return p?.close();
      const width = p.displayWidth ?? c.width, height = p.displayHeight ?? c.height;
      if (c.width !== width || c.height !== height) Object.assign(c, { width, height });
      c.getContext("2d")?.drawImage(p as unknown as CanvasImageSource, 0, 0, width, height);
      p.close();
      place();
    };
    link.onPicture = (p) => {
      latest?.close();
      latest = p;
      if (!frame) frame = requestAnimationFrame(draw);
    };
    link.onShowing = (on) => {
      holdUpdates(rfb, on);
      const v = vnc();
      if (v) v.style.opacity = on ? "0" : "";
      if (canvas.current) canvas.current.style.display = on ? "" : "none";
      if (on) place();
    };
    const watch = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(place);
    watch?.observe(screen);
    if (canvas.current?.parentElement) watch?.observe(canvas.current.parentElement);
    return () => {
      watch?.disconnect();
      if (frame) cancelAnimationFrame(frame);
      latest?.close();
      link.onPicture = (p) => p.close();
      link.onShowing = () => {};
      holdUpdates(rfb, false);
      const v = vnc();
      if (v) v.style.opacity = "";
    };
  }, [link, rfb, screen]);

  return <canvas ref={canvas} data-testid="screen-video-picture" aria-hidden="true" className="pointer-events-none absolute" style={{ display: "none" }} />;
}

const LINES: Record<Fallback, () => string> = {
  lost: () => t("screen-video.fallback_lost"),
  locked: () => t("screen-video.fallback_locked"),
  notAllowed: () => t("screen-video.fallback_not_allowed"),
};

/** The line that says why VNC shows while video was chosen; for `notAllowed`, with "How to turn it on": one sentence,
 *  a window to answer when macOS hasn't asked yet (`asked` false), else where in System Settings. */
export function FallbackLine({ fallback, asked }: { fallback: Fallback; asked: boolean }) {
  const [how, setHow] = useState(false);
  const words = LINES[fallback]();
  return (
    <>
      {fallback === "notAllowed" ? (
        <button type="button" onClick={() => setHow(true)} data-testid="screen-fallback-help" className="rounded-control bg-surface/90 px-3 py-1.5 text-label text-ink-2 shadow-overlay backdrop-blur-md">
          {words} <span className="text-accent">{t("screen-video.how_to_turn_on")}</span>
        </button>
      ) : (
        <p role="status" data-testid="screen-fallback" className="rounded-control bg-surface/90 px-3 py-1.5 text-label text-ink-2 shadow-overlay backdrop-blur-md">
          {words}
        </p>
      )}
      <Dialog open={how} onClose={() => setHow(false)} title={t("screen-video.turn_on_title")}>
        <p className="text-meta text-ink-2">{asked ? t("screen-video.how_settings") : t("screen-video.how_window")}</p>
      </Dialog>
    </>
  );
}
