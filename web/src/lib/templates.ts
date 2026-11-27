// Templates (#16772, docs/web/web.md "Templates"): a named, versioned set of configuration applied in one step from
// Settings › General › Templates, and handed to another Persona as a link (`/settings?template=…`). A template has up
// to four parts, each applied where it belongs:
//   - browser: this browser's looks (phone navigation, sidebar icons, the lean looks of lib/looks.ts, optional web
//     modules switched off);
//   - navigation: the hub's start page and sidebar page arrangement (`PUT /api/v1/navigation`, every device follows);
//   - extensions: per installed extension, whether it is on and part of its configuration (its own routes);
//   - settings: persona_settings keys, checked and written by the manager (`POST /api/v1/templates/settings`).
// The format is `schema/template.schema.json`. A template never carries secrets: extension secrets have their own
// routes, and the manager refuses settings that grant permissions or speak for the owner.
// Parsing, links and the plan are pure; applying takes its writers as arguments so it can be tested without a hub.
import { isLookMode, LOOK_NAMES, LOOKS, type LookMode, type LookName } from "./looks";
import { PHONE_NAV_MODES, type PhoneNavMode } from "./phone-nav";
import { SIDEBAR_ICON_MODES, type SidebarIconMode } from "./sidebar-icons";
import { readPrefs, type SidebarPrefs } from "./sidebar-pages";
import type { Json, JsonObject } from "./extensions";

export const TEMPLATE_VERSION = 1;
export const TEMPLATE_KEY = "persona.template";

export type TemplateLang = "en" | "zh-Hans" | "ja";
/** A name or description: one string, or one per language. */
export type Localized = string | Partial<Record<TemplateLang, string>>;
export type SettingAction = "set" | "add" | "remove";

export interface TemplateSetting {
  key: string;
  action?: SettingAction;
  value: Json;
}

export interface PersonaTemplate {
  persona_template: typeof TEMPLATE_VERSION;
  id: string;
  name: Localized;
  description?: Localized;
  browser?: { phone_nav?: PhoneNavMode; sidebar_icons?: SidebarIconMode; modules_off?: string[] } & { [N in LookName]?: LookMode<N> };
  /** `pages: null` puts the sidebar back to its default arrangement. */
  navigation?: { start?: string; pages?: SidebarPrefs | null };
  extensions?: Record<string, { enabled?: boolean; config?: JsonObject }>;
  settings?: TemplateSetting[];
}

const ID = /^[a-z0-9][a-z0-9-]{0,63}$/;
const START = /^(?:[a-z][a-z0-9-]{0,63}|p:[^/\s]{1,128}\/[^/\s]{1,128})?$/;
const EXTENSION_ID = /^[a-z0-9][a-z0-9._-]{0,63}$/;
const MAX_SETTINGS = 64;

const isObject = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);

function localizedOk(v: unknown, max: number): boolean {
  if (typeof v === "string") return v.trim().length > 0 && v.length <= max;
  return isObject(v) && Object.keys(v).length > 0
    && Object.entries(v).every(([k, s]) => ["en", "zh-Hans", "ja"].includes(k) && typeof s === "string" && s.trim().length > 0 && s.length <= max);
}

/** Reads a template from what a link or a paste carried; the error says what is wrong in plain words for the owner. */
export function parseTemplate(raw: unknown): { template: PersonaTemplate } | { error: string } {
  if (!isObject(raw)) return { error: "not a template" };
  if (raw.persona_template !== TEMPLATE_VERSION) return { error: "unsupported template version" };
  if (typeof raw.id !== "string" || !ID.test(raw.id)) return { error: "id must be lowercase letters, digits and dashes" };
  if (!localizedOk(raw.name, 80)) return { error: "name is required" };
  if (raw.description !== undefined && !localizedOk(raw.description, 400)) return { error: "description must be text" };
  const known = new Set(["persona_template", "id", "name", "description", "browser", "navigation", "extensions", "settings"]);
  const unknown = Object.keys(raw).find((k) => !known.has(k));
  if (unknown) return { error: `unknown part: ${unknown}` };
  const t: PersonaTemplate = { persona_template: TEMPLATE_VERSION, id: raw.id, name: raw.name as Localized };
  if (raw.description !== undefined) t.description = raw.description as Localized;

  if (raw.browser !== undefined) {
    const b = raw.browser;
    if (!isObject(b)) return { error: "browser must be an object" };
    t.browser = {};
    if (b.phone_nav !== undefined) {
      if (!(PHONE_NAV_MODES as readonly unknown[]).includes(b.phone_nav)) return { error: "browser.phone_nav must be switch or tabs" };
      t.browser.phone_nav = b.phone_nav as PhoneNavMode;
    }
    if (b.sidebar_icons !== undefined) {
      if (!(SIDEBAR_ICON_MODES as readonly unknown[]).includes(b.sidebar_icons)) return { error: "browser.sidebar_icons must be monochrome or colored" };
      t.browser.sidebar_icons = b.sidebar_icons as SidebarIconMode;
    }
    for (const name of LOOK_NAMES) {
      if (b[name] === undefined) continue;
      if (!isLookMode(name, b[name])) return { error: `browser.${name} must be ${LOOKS[name].join(" or ")}` };
      (t.browser as Record<string, unknown>)[name] = b[name];
    }
    if (b.modules_off !== undefined) {
      if (!Array.isArray(b.modules_off) || b.modules_off.length > 32 || !b.modules_off.every((m) => typeof m === "string" && ID.test(m))) return { error: "browser.modules_off must list module ids" };
      t.browser.modules_off = [...new Set(b.modules_off as string[])];
    }
  }

  if (raw.navigation !== undefined) {
    const n = raw.navigation;
    if (!isObject(n)) return { error: "navigation must be an object" };
    t.navigation = {};
    if (n.start !== undefined) {
      if (typeof n.start !== "string" || !START.test(n.start)) return { error: "navigation.start must be a page's key" };
      t.navigation.start = n.start;
    }
    if (n.pages !== undefined) {
      if (n.pages !== null && !isObject(n.pages)) return { error: "navigation.pages must be an arrangement or null" };
      t.navigation.pages = n.pages === null ? null : readPrefs(n.pages);
    }
  }

  if (raw.extensions !== undefined) {
    const e = raw.extensions;
    if (!isObject(e) || Object.keys(e).length > 64) return { error: "extensions must be an object of at most 64 extensions" };
    t.extensions = {};
    for (const [id, part] of Object.entries(e)) {
      if (!EXTENSION_ID.test(id) || !isObject(part)) return { error: `extensions.${id} is not valid` };
      const out: { enabled?: boolean; config?: JsonObject } = {};
      if (part.enabled !== undefined) {
        if (typeof part.enabled !== "boolean") return { error: `extensions.${id}.enabled must be true or false` };
        out.enabled = part.enabled;
      }
      if (part.config !== undefined) {
        if (!isObject(part.config)) return { error: `extensions.${id}.config must be an object` };
        out.config = part.config as JsonObject;
      }
      t.extensions[id] = out;
    }
  }

  if (raw.settings !== undefined) {
    const s = raw.settings;
    if (!Array.isArray(s) || s.length > MAX_SETTINGS) return { error: `settings must be a list of at most ${MAX_SETTINGS}` };
    t.settings = [];
    for (const item of s) {
      if (!isObject(item) || typeof item.key !== "string" || !item.key.trim() || item.key.length > 128 || !("value" in item)) return { error: "each setting needs a key and a value" };
      if (item.action !== undefined && !["set", "add", "remove"].includes(item.action as string)) return { error: `${item.key}: action must be set, add or remove` };
      t.settings.push({ key: item.key, ...(item.action ? { action: item.action as SettingAction } : {}), value: item.value as Json });
    }
  }
  return { template: t };
}

/** The name or description in `lang`, else English, else any. */
export function localized(v: Localized | undefined, lang: string): string {
  if (v === undefined) return "";
  if (typeof v === "string") return v;
  return v[lang as TemplateLang] ?? v.en ?? Object.values(v).find(Boolean) ?? "";
}

// ── links: a built-in by its id, any other template as its JSON in base64url ──

function toBase64Url(s: string): string {
  const bytes = new TextEncoder().encode(s);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function fromBase64Url(s: string): string {
  const bin = atob(s.replace(/-/g, "+").replace(/_/g, "/"));
  return new TextDecoder().decode(Uint8Array.from(bin, (c) => c.charCodeAt(0)));
}

/** What `?template=` carries for a template: a built-in's id, else the whole template. */
export function templateParam(t: PersonaTemplate, builtIn: boolean): string {
  return builtIn ? t.id : toBase64Url(JSON.stringify(t));
}

export function templateLink(origin: string, t: PersonaTemplate, builtIn: boolean): string {
  return `${origin}/settings?tab=general&template=${encodeURIComponent(templateParam(t, builtIn))}`;
}

/** Reads `?template=`, a pasted link or pasted JSON: a built-in's id (looked up in `builtIns`), or a template. */
export function readTemplateInput(input: string, builtIns: readonly PersonaTemplate[]): { template: PersonaTemplate; builtIn: boolean } | { error: string } {
  let v = input.trim();
  if (!v) return { error: "empty" };
  if (v.startsWith("{")) {
    try {
      const r = parseTemplate(JSON.parse(v));
      return "error" in r ? r : { template: r.template, builtIn: false };
    } catch {
      return { error: "not valid JSON" };
    }
  }
  try {
    const u = new URL(v);
    v = u.searchParams.get("template") ?? "";
    if (!v) return { error: "the link carries no template" };
  } catch {
    // not a link: the parameter itself
  }
  const builtIn = builtIns.find((b) => b.id === v);
  if (builtIn) return { template: builtIn, builtIn: true };
  try {
    const r = parseTemplate(JSON.parse(fromBase64Url(v)));
    return "error" in r ? r : { template: r.template, builtIn: false };
  } catch {
    return { error: "not a template link" };
  }
}

// ── the built-ins ──

/** The built-in templates: Default, Persona as it comes (the top switch, the full main chat, Agents and Settings, every
 *  optional web module on, the sidebar's default pages). Any other setup is a template of its own, shared as a link or JSON. Names come from the catalog. */
export function builtInTemplates(name: (key: string) => string): PersonaTemplate[] {
  return [
    {
      persona_template: TEMPLATE_VERSION,
      id: "default",
      name: name("default_name"),
      description: name("default_description"),
      browser: { phone_nav: "switch", sidebar_icons: "monochrome", main_chat: "full", agents_view: "full", settings_view: "full", modules_off: [] },
      navigation: { pages: null },
    },
  ];
}

// ── applying ──

export type ItemStatus = "change" | "unchanged" | "applied" | "failed" | "skipped";

/** One line of a template's preview or result. */
export interface TemplateItem {
  part: "browser" | "navigation" | "extensions" | "settings";
  /** what it is, in the owner's words (a setting's title, an extension's name) */
  label: string;
  /** before → after, or why it was skipped or failed */
  detail?: string;
  status: ItemStatus;
}

/** What the plan and the apply read and write; the view passes the real ones, tests fake them. */
export interface TemplateHost {
  phoneNav(): PhoneNavMode;
  setPhoneNav(m: PhoneNavMode): void;
  sidebarIcons(): SidebarIconMode;
  setSidebarIcons(m: SidebarIconMode): void;
  look<N extends LookName>(name: N): LookMode<N>;
  setLook<N extends LookName>(name: N, mode: LookMode<N>): void;
  /** the optional web modules this browser can switch, and which are off */
  modules(): { id: string; title: string; off: boolean }[];
  setModuleOff(id: string, off: boolean): void;
  startPage(): string;
  saveStartPage(key: string): Promise<unknown>;
  sidebarPrefs(): SidebarPrefs;
  setSidebarPrefs(p: SidebarPrefs | null): void;
  /** an installed extension's name, switch and configuration; undefined when it is not installed */
  extension(id: string): Promise<{ name: string; enabled: boolean; config: JsonObject } | undefined>;
  setExtensionEnabled(id: string, on: boolean): Promise<unknown>;
  setExtensionConfig(id: string, config: JsonObject): Promise<unknown>;
  /** POST /api/v1/templates/settings; undefined from a hub without the route */
  settings(items: TemplateSetting[], dryRun: boolean): Promise<{ key: string; title?: string; status: string; text?: string; error?: string }[] | undefined>;
  words: TemplateWords;
}

/** The labels and status words the plan uses, from the view's catalog. */
export interface TemplateWords {
  phoneNav: string;
  phoneNavMode: (m: PhoneNavMode) => string;
  sidebarIcons: string;
  sidebarIconsMode: (m: SidebarIconMode) => string;
  look: (name: LookName) => string;
  lookMode: (name: LookName, mode: string) => string;
  module: (title: string) => string;
  on: string;
  off: string;
  startPage: string;
  startDefault: string;
  sidebarPages: string;
  pagesDefault: string;
  pagesUnderMore: (n: number) => string;
  pagesCustom: string;
  extensionMissing: string;
  extensionEnabled: string;
  extensionConfig: (keys: string) => string;
  settingsUnsupportedHub: string;
  settingStatus: (status: string) => string;
}

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
const arrow = (a: string, b: string) => `${a} → ${b}`;

function pagesWords(p: SidebarPrefs | null, w: TemplateWords): string {
  if (!p || (!p.order.length && !p.shown.length && !p.hidden.length && !p.groups?.length && p.more === undefined)) return w.pagesDefault;
  if (!p.order.length && !p.shown.length && !p.groups?.length) return w.pagesUnderMore(p.hidden.length);
  return w.pagesCustom;
}

/** What applying `t` would do, part by part, without writing anything (the settings part asks the manager). */
export async function planTemplate(t: PersonaTemplate, host: TemplateHost): Promise<TemplateItem[]> {
  return run(t, host, true);
}

/** Applies `t` part by part and says what happened to each item; a failing item does not stop the rest. */
export async function applyTemplate(t: PersonaTemplate, host: TemplateHost): Promise<TemplateItem[]> {
  return run(t, host, false);
}

async function run(t: PersonaTemplate, host: TemplateHost, dry: boolean): Promise<TemplateItem[]> {
  const w = host.words;
  const out: TemplateItem[] = [];
  const done = (changed: boolean): ItemStatus => (changed ? (dry ? "change" : "applied") : "unchanged");
  const attempt = async (item: Omit<TemplateItem, "status">, changed: boolean, write: () => unknown) => {
    if (!changed || dry) return out.push({ ...item, status: done(changed) });
    try {
      await write();
      out.push({ ...item, status: "applied" });
    } catch (e) {
      out.push({ ...item, status: "failed", detail: e instanceof Error ? e.message : String(e) });
    }
  };

  const b = t.browser;
  if (b?.phone_nav) {
    const now = host.phoneNav();
    await attempt({ part: "browser", label: w.phoneNav, detail: arrow(w.phoneNavMode(now), w.phoneNavMode(b.phone_nav)) }, now !== b.phone_nav, () => host.setPhoneNav(b.phone_nav!));
  }
  if (b?.sidebar_icons) {
    const now = host.sidebarIcons();
    await attempt({ part: "browser", label: w.sidebarIcons, detail: arrow(w.sidebarIconsMode(now), w.sidebarIconsMode(b.sidebar_icons)) }, now !== b.sidebar_icons, () => host.setSidebarIcons(b.sidebar_icons!));
  }
  for (const name of LOOK_NAMES) {
    const wanted = b?.[name];
    if (!wanted) continue;
    const now = host.look(name);
    await attempt({ part: "browser", label: w.look(name), detail: arrow(w.lookMode(name, now), w.lookMode(name, wanted)) }, now !== wanted, () => host.setLook(name, wanted));
  }
  if (b?.modules_off) {
    const wanted = new Set(b.modules_off);
    for (const m of host.modules()) {
      const off = wanted.has(m.id);
      await attempt({ part: "browser", label: w.module(m.title), detail: arrow(m.off ? w.off : w.on, off ? w.off : w.on) }, m.off !== off, () => host.setModuleOff(m.id, off));
    }
  }

  const n = t.navigation;
  if (n?.start !== undefined) {
    const now = host.startPage();
    await attempt({ part: "navigation", label: w.startPage, detail: arrow(now || w.startDefault, n.start || w.startDefault) }, now !== n.start, () => host.saveStartPage(n.start!));
  }
  if (n?.pages !== undefined) {
    const now = host.sidebarPrefs();
    const target = n.pages;
    const changed = target === null ? pagesWords(now, w) !== w.pagesDefault : !same(now, target);
    await attempt({ part: "navigation", label: w.sidebarPages, detail: arrow(pagesWords(now, w), pagesWords(target, w)) }, changed, () => host.setSidebarPrefs(target));
  }

  for (const [id, part] of Object.entries(t.extensions ?? {})) {
    let ext: Awaited<ReturnType<TemplateHost["extension"]>>;
    try {
      ext = await host.extension(id);
    } catch (e) {
      out.push({ part: "extensions", label: id, status: "failed", detail: e instanceof Error ? e.message : String(e) });
      continue;
    }
    if (!ext) {
      out.push({ part: "extensions", label: id, status: "skipped", detail: w.extensionMissing });
      continue;
    }
    const found = ext;
    if (part.enabled !== undefined) {
      await attempt({ part: "extensions", label: found.name, detail: arrow(w.extensionEnabled + " " + (found.enabled ? w.on : w.off), part.enabled ? w.on : w.off) },
        found.enabled !== part.enabled, () => host.setExtensionEnabled(id, part.enabled!));
    }
    if (part.config) {
      const changed = Object.keys(part.config).some((k) => !same(found.config[k], part.config![k]));
      await attempt({ part: "extensions", label: found.name, detail: w.extensionConfig(Object.keys(part.config).join(", ")) },
        changed, () => host.setExtensionConfig(id, { ...found.config, ...part.config }));
    }
  }

  if (t.settings?.length) {
    let answer: Awaited<ReturnType<TemplateHost["settings"]>>;
    try {
      answer = await host.settings(t.settings, dry);
    } catch (e) {
      for (const s of t.settings) out.push({ part: "settings", label: s.key, status: "failed", detail: e instanceof Error ? e.message : String(e) });
      return out;
    }
    if (!answer) {
      for (const s of t.settings) out.push({ part: "settings", label: s.key, status: "skipped", detail: w.settingsUnsupportedHub });
      return out;
    }
    for (const r of answer) {
      const status: ItemStatus = r.status === "change" || r.status === "unchanged" || r.status === "applied" || r.status === "failed" ? r.status : "skipped";
      out.push({ part: "settings", label: r.title || r.key, status, detail: r.error || r.text || (status === "skipped" ? w.settingStatus(r.status) : undefined) });
    }
  }
  return out;
}
