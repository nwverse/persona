// The DOM side of the attention signals (#2332, logic in lib/attention.ts): this first-screen shell handles the tab
// title, favicon badge, and desktop Dock badge immediately, while loading the heavy background notification, audio,
// desktop presence, Web Push, and Service Worker reply runtime asynchronously (lib/attention-signals-runtime.ts).
// `<AttentionSignals>` is what App renders for them.
import { useEffect, useRef } from "react";
import { badgeText, tabTitle } from "./attention";
import { useAgentSessions, useCounts } from "./sessions";
import { onHello, useStore } from "./store";
import type { AttentionRuntime, AttentionSnapshot } from "./attention-signals-runtime";

// ── favicon ──────────────────────────────────────────────

const ICONS = 'link[rel="icon"]';
const SIZE = 64; // the icon files are 64 px; the browser scales the tab's down
const decoded = new Map<string, Promise<HTMLImageElement>>();
const drawn = new Map<string, string>();

function load(src: string): Promise<HTMLImageElement> {
  let p = decoded.get(src);
  if (!p) {
    p = new Promise((resolve, reject) => {
      const img = new Image();
      img.onload = () => resolve(img);
      img.onerror = () => reject(new Error("icon"));
      img.src = src;
    });
    decoded.set(src, p);
    p.catch(() => decoded.delete(src));
  }
  return p;
}

/** The icon with the "Needs you" badge at its top right: a red disc (the sidebar's --status-needs) with the count. */
async function badged(src: string, text: string): Promise<string> {
  const key = `${src}|${text}`;
  const hit = drawn.get(key);
  if (hit) return hit;
  const img = await load(src);
  const canvas = document.createElement("canvas");
  canvas.width = canvas.height = SIZE;
  const g = canvas.getContext("2d");
  if (!g) throw new Error("canvas");
  g.drawImage(img, 0, 0, SIZE, SIZE);
  const css = getComputedStyle(document.documentElement);
  const r = SIZE * 0.34;
  const cx = SIZE - r;
  const cy = r;
  g.beginPath();
  g.arc(cx, cy, r, 0, Math.PI * 2);
  g.fillStyle = css.getPropertyValue("--status-needs").trim() || css.getPropertyValue("--red").trim() || "red";
  g.fill();
  g.lineWidth = SIZE * 0.05;
  g.strokeStyle = css.getPropertyValue("--surface").trim() || "white";
  g.stroke();
  g.fillStyle = "white";
  g.font = `700 ${text.length > 1 ? r * 1.05 : r * 1.35}px system-ui, sans-serif`;
  g.textAlign = "center";
  g.textBaseline = "middle";
  g.fillText(text, cx, cy + r * 0.06);
  const url = canvas.toDataURL("image/png");
  drawn.set(key, url);
  return url;
}

let faviconSeq = 0;
let faviconNeeds = 0;

/** The look's icon (lib/look-icon.ts, #3200) under the badge: each link's original becomes the look's picture for its
 *  scheme (index.html's light and dark links), or the files index.html names again with null. */
export function setFaviconBase(icon: { light: string; dark: string } | null): void {
  for (const link of document.querySelectorAll<HTMLLinkElement>(ICONS)) {
    link.dataset.file ??= link.dataset.original ?? link.getAttribute("href") ?? "";
    const dark = (link.getAttribute("media") ?? "").includes("dark");
    link.dataset.original = icon ? (dark ? icon.dark : icon.light) : link.dataset.file;
  }
  setFaviconBadge(faviconNeeds);
}

/** Swaps the page's icon links (index.html has a light and a dark one, chosen by the browser's media query) for the
 *  badged copy of each, or back to the originals at 0. The files themselves come through the service worker like any
 *  other icon (lib/sw-routes.ts "static": the hub's, else the cached one), so this also works offline; the drawn
 *  copies are data: URLs and never touch the network or the cache. */
export function setFaviconBadge(needs: number): void {
  faviconNeeds = needs;
  const links = [...document.querySelectorAll<HTMLLinkElement>(ICONS)];
  const text = badgeText(needs);
  const seq = ++faviconSeq;
  for (const link of links) {
    link.dataset.original ??= link.getAttribute("href") ?? "";
    const original = link.dataset.original;
    if (!text) {
      link.setAttribute("href", original);
      continue;
    }
    badged(original, text).then(
      (url) => seq === faviconSeq && link.setAttribute("href", url),
      () => seq === faviconSeq && link.setAttribute("href", original), // the icon can't be read: leave it as it is
    );
  }
}

// ── lazy background runtime ──────────────────────────────

type AttentionRuntimeModule = typeof import("./attention-signals-runtime");

let loadedRuntime: AttentionRuntimeModule | null = null;
let runtimePromise: Promise<AttentionRuntimeModule> | null = null;

function loadAttentionRuntime(): Promise<AttentionRuntimeModule> {
  if (loadedRuntime) return Promise.resolve(loadedRuntime);
  return (runtimePromise ??= import("./attention-signals-runtime").then(
    (mod) => {
      loadedRuntime = mod;
      return mod;
    },
    (err) => {
      runtimePromise = null;
      throw err;
    },
  ));
}

/** Loads the background attention-signals runtime chunk if not already loaded. */
export function preloadAttentionSignals(): Promise<void> {
  return loadAttentionRuntime().then(() => undefined);
}

/** Arms the alert sound's AudioContext on the first user gesture when alert sounds are enabled. */
export function armAudio(): () => void {
  if (loadedRuntime) return loadedRuntime.armAudio();
  let stopped = false;
  let stop: (() => void) | undefined;
  void loadAttentionRuntime().then(
    (mod) => {
      if (!stopped) stop = mod.armAudio();
    },
    () => {},
  );
  return () => {
    stopped = true;
    stop?.();
  };
}

/** Plays the alert chime or soft completion note if audio is armed and running. */
export function play(kind: "needs" | "finished"): void {
  if (loadedRuntime) {
    loadedRuntime.play(kind);
    return;
  }
  void loadAttentionRuntime().then((mod) => mod.play(kind), () => {});
}

// ── the hook ─────────────────────────────────────────────

const MAX_PENDING_SNAPSHOTS = 32;

/**
 * The tab title (`label` is the page's label, as App works it out), the favicon badge, the sounds and the system
 * notifications. `pane` is the agent conversation on the page, if any: it is not announced while the window is focused.
 * Sounds and notifications follow this browser's Settings › Notifications (lib/notify.ts): "Needs you" by its switch,
 * "Done" by its three choices, a turn's length as the page saw it (trackTurns).
 */
export function useAttentionSignals(label: string, pane: { mid: string; sid: string } | undefined): void {
  const needs = useCounts().badge;
  useEffect(() => {
    document.title = tabTitle(label, needs);
  }, [label, needs]);
  useEffect(() => {
    setFaviconBadge(needs);
    window.personaDesktop?.setBadge(needs);
  }, [needs]);

  const sessions = useAgentSessions();
  const loaded = useStore((s) => s.loaded);
  const interactions = useStore((s) => s.interactions);
  const pushGrading = Boolean(useStore((s) => s.pushGrading));

  const latest = useRef<AttentionSnapshot>({ pane, sessions, interactions, loaded, pushGrading });
  latest.current = { pane, sessions, interactions, loaded, pushGrading };
  const runtime = useRef<AttentionRuntime | null>(null);
  const pending = useRef<AttentionSnapshot[]>([]);
  const startRef = useRef<(() => void) | null>(null);

  useEffect(() => {
    let alive = true;
    let loading = false;
    let instance: AttentionRuntime | null = null;
    let sawHello = false;
    let offEarlyHello: (() => void) | undefined = onHello(() => {
      sawHello = true;
      if (!instance && !loading) start();
    });
    const mount = (mod: AttentionRuntimeModule) => {
      offEarlyHello?.();
      offEarlyHello = undefined;
      const queued = pending.current;
      if (queued.length) {
        instance = mod.createAttentionRuntime(queued[0], { sawHello });
        for (let i = 1; i < queued.length; i++) {
          const snap = queued[i];
          instance.setPane(snap.pane);
          instance.syncLoaded(snap.loaded);
          instance.update(snap.sessions, snap.interactions, snap.loaded, snap.pushGrading);
        }
        instance.setPane(latest.current.pane);
        queued.length = 0;
      } else {
        instance = mod.createAttentionRuntime(latest.current, { sawHello });
      }
      runtime.current = instance;
    };
    const start = () => {
      if (!alive || instance || loading) return;
      if (loadedRuntime) {
        mount(loadedRuntime);
        return;
      }
      loading = true;
      void loadAttentionRuntime().then(
        (mod) => {
          loading = false;
          if (!alive) return;
          mount(mod);
        },
        () => {
          loading = false;
          pending.current.length = 0;
        },
      );
    };
    startRef.current = start;
    start();
    return () => {
      alive = false;
      startRef.current = null;
      pending.current.length = 0;
      offEarlyHello?.();
      offEarlyHello = undefined;
      if (instance) {
        if (runtime.current === instance) runtime.current = null;
        instance.dispose();
        instance = null;
      }
    };
  }, []);

  useEffect(() => {
    runtime.current?.setPane(pane);
  }, [pane]);

  useEffect(() => {
    if (loaded && !runtime.current) startRef.current?.();
    runtime.current?.syncLoaded(loaded);
  }, [loaded]);

  useEffect(() => {
    if (!runtime.current) {
      if (loaded) {
        const queue = pending.current;
        const snap: AttentionSnapshot = { pane: latest.current.pane, sessions, interactions, loaded, pushGrading };
        if (queue.length < MAX_PENDING_SNAPSHOTS) queue.push(snap);
        else queue[queue.length - 1] = snap;
      }
      return;
    }
    runtime.current.update(sessions, interactions, loaded, pushGrading);
  }, [sessions, interactions, loaded, pushGrading]);
}

/**
 * `useAttentionSignals` as a component that draws nothing. It reads every session push (the "Needs you" count, the
 * transitions): rendered as a leaf, a push re-renders only it, not the shell with the sidebar and the page around it.
 */
export function AttentionSignals({ label, pane }: { label: string; pane: { mid: string; sid: string } | undefined }): null {
  useAttentionSignals(label, pane);
  return null;
}
