// The look's icon (#3200, docs/extensions/extensions.md "Avatars"): the picture the page's mark (AppMark: the sidebar's header, the
// sign-in page) and the tab's favicon show for the chosen look. Mochi, the default, is the app icon itself (the files
// in public/); another look uses its package's PNG or SVG, light and dark, made into a 128 px PNG. The last one is kept
// in localStorage, so the sign-in page and the first paint show it before the
// hub answers. The installed app's icon (the PWA's manifest) stays the app icon: it is fixed when the app is installed.
import { useSyncExternalStore } from "react";
import { getToken } from "./api";
import { setFaviconBase } from "./attention-signals";
import { avatarFile, BUILTIN_PERSONA, normalizePersonaChoice, personaFileURL, type PersonaInfo } from "./persona";

const KEY = "persona.look-icon";
const SIDE = 128;

export interface LookIcon {
  /** What it is made from (the look and its rev), so the same look is not drawn again. */
  key: string;
  light: string;
  dark: string;
}

let icon: LookIcon | null = read();
const listeners = new Set<() => void>();
let seq = 0;

function read(): LookIcon | null {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? "null");
    const chosen = normalizePersonaChoice(localStorage.getItem("persona.persona") ?? BUILTIN_PERSONA);
    if (chosen === BUILTIN_PERSONA || typeof v?.key !== "string" || !v.key.startsWith(`${chosen}@`)) {
      localStorage.removeItem(KEY);
      return null;
    }
    return v && typeof v.light === "string" && typeof v.dark === "string" && v.light.startsWith("data:image/") && v.dark.startsWith("data:image/") ? v : null;
  } catch {
    return null;
  }
}

function set(next: LookIcon | null) {
  icon = next;
  try {
    if (next) localStorage.setItem(KEY, JSON.stringify(next));
    else localStorage.removeItem(KEY);
  } catch {
    // private mode: this page only
  }
  if (typeof document !== "undefined") setFaviconBase(next && { light: next.light, dark: next.dark });
  listeners.forEach((l) => l());
}

/** The picture at src as a SIDE px PNG (an SVG drawn at that size). */
function rasterize(src: string): Promise<string> {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.onload = () => {
      const c = document.createElement("canvas");
      c.width = c.height = SIDE;
      const g = c.getContext("2d");
      if (!g) return reject(new Error("canvas"));
      g.drawImage(img, 0, 0, SIDE, SIDE);
      try {
        resolve(c.toDataURL("image/png"));
      } catch (e) {
        reject(e);
      }
    };
    img.onerror = () => reject(new Error("icon"));
    img.src = src;
  });
}

/** The package's avatar supplies the icon; every missing or retired look uses the default app icon. */
export function setLookIcon(info: PersonaInfo | null): void {
  if (typeof document === "undefined" || typeof Image === "undefined") return;
  if (!info) {
    ++seq; // Cancel a previous package's rasterization even when there is no cached icon yet.
    if (icon) set(null);
    return;
  }
  const key = `${info.id}@${info.rev}`;
  if (icon?.key === key) return;
  const mine = ++seq;
  const made = Promise.all([false, true].map((dark) => {
    const f = avatarFile(info.manifest, SIDE, dark);
    return f ? rasterize(personaFileURL(info, f, getToken())) : Promise.reject(new Error("no avatar"));
  }));
  made.then(
    ([light, dark]) => mine === seq && set({ key, light, dark }),
    () => undefined,
  );
}

export function useLookIcon(): LookIcon | null {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => icon,
    // a static render (the row tests) draws the app icon
    () => null,
  );
}

// The page starts with the last look's icon in the tab (index.html names the app icon's files).
if (typeof document !== "undefined" && icon) setFaviconBase({ light: icon.light, dark: icon.dark });
