// Installing the web client as an app (PWA, docs/web/web.md "Install as an app"): registers the service worker (src/sw.ts) and
// tracks what Settings › General can offer — the browser's own install prompt, a hint where there is none, or "Installed".
import { useSyncExternalStore } from "react";
import { BASE } from "./base";

export type InstallState =
  | "installed" // running as the installed app
  | "prompt" // the browser offered its install prompt (Chrome, Edge): one click
  | "ios" // iPhone / iPad: Safari's share menu › Add to Home Screen
  | "safari" // Safari on a Mac: File › Add to Dock
  | "insecure" // other browsers install only from https (or localhost)
  | "menu"; // the browser's own menu, if it can

export function installState(env: { standalone: boolean; installed: boolean; prompt: boolean; secure: boolean; ua: string; touchPoints: number }): InstallState {
  if (env.standalone || env.installed) return "installed";
  if (env.prompt) return "prompt";
  // iPadOS asks for the desktop site: a "Macintosh" with a touch screen.
  if (/iPhone|iPad|iPod/.test(env.ua) || (/Macintosh/.test(env.ua) && env.touchPoints > 1)) return "ios";
  if (/Macintosh/.test(env.ua) && /Version\/[\d.]+.*Safari\//.test(env.ua) && !/Chrome|Chromium|Edg\//.test(env.ua)) return "safari";
  if (!env.secure) return "insecure";
  return "menu";
}

interface InstallPrompt extends Event {
  prompt(): Promise<void>;
  userChoice: Promise<{ outcome: "accepted" | "dismissed" }>;
}

let deferred: InstallPrompt | null = null;
let installed = false;
const listeners = new Set<() => void>();
const changed = () => listeners.forEach((l) => l());
const standalone = () => matchMedia("(display-mode: standalone)").matches || (navigator as { standalone?: boolean }).standalone === true;

/** From main.tsx, before the first render: the install prompt comes early and only once per page load. */
export function startPWA() {
  addEventListener("beforeinstallprompt", (e) => {
    e.preventDefault(); // offered in Settings, not as the browser's own banner
    deferred = e as InstallPrompt;
    changed();
  });
  addEventListener("appinstalled", () => {
    deferred = null;
    installed = true;
    changed();
  });
  matchMedia("(display-mode: standalone)").addEventListener("change", changed);
  // Service workers need a secure context; over the LAN's plain http the page stays a plain page. Not in dev: Vite's
  // modules aren't the build's.
  // Not under a sub-path (lib/base.ts): the worker's routes and caches are the root's (docs/web/web.md "Subpaths").
  if (import.meta.env.PROD && !BASE && isSecureContext && "serviceWorker" in navigator) {
    afterLoad(() => void navigator.serviceWorker.register("/sw.js", { updateViaCache: "none" }).catch(() => {}));
  }
}

/** Runs `run` once the page has loaded: now if it already has. main.tsx awaits its imports before startPWA, by which
 * time `load` has usually fired, so a listener alone would never run (#4724). */
export function afterLoad(
  run: () => void,
  doc: Pick<Document, "readyState"> = document,
  win: Pick<Window, "addEventListener"> = window,
) {
  if (doc.readyState === "complete") run();
  else win.addEventListener("load", run, { once: true });
}

const current = (): InstallState =>
  installState({ standalone: standalone(), installed, prompt: !!deferred, secure: isSecureContext, ua: navigator.userAgent, touchPoints: navigator.maxTouchPoints });

export function useInstall(): InstallState {
  return useSyncExternalStore((l) => {
    listeners.add(l);
    return () => listeners.delete(l);
  }, current);
}

/** Shows the browser's install prompt; false when it isn't there or the user said no. */
export async function install(): Promise<boolean> {
  const p = deferred;
  if (!p) return false;
  deferred = null; // a prompt can be shown once
  await p.prompt();
  const { outcome } = await p.userChoice;
  changed();
  return outcome === "accepted";
}
