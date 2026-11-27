// Templates (#16772, lib/templates.ts): the format, links, the built-ins, and a plan and an apply against a fake host.
import { describe, expect, it } from "vitest";
import type { PhoneNavMode } from "./phone-nav";
import type { SidebarIconMode } from "./sidebar-icons";
import type { SidebarPrefs } from "./sidebar-pages";
import {
  applyTemplate, builtInTemplates, localized, parseTemplate, planTemplate, readTemplateInput, templateLink,
  type PersonaTemplate, type TemplateHost, type TemplateSetting,
} from "./templates";

const NONE: SidebarPrefs = { order: [], shown: [], hidden: [] };

function fakeHost(o: { extensions?: Record<string, { name: string; enabled: boolean; config: Record<string, unknown> }>; settings?: "missing" | "fail" } = {}) {
  const state = {
    phoneNav: "switch" as PhoneNavMode,
    icons: "monochrome" as SidebarIconMode,
    off: new Set<string>(["voice"]),
    start: "",
    prefs: NONE as SidebarPrefs,
    ext: structuredClone(o.extensions ?? {}),
    sent: [] as { items: TemplateSetting[]; dry: boolean }[],
    writes: 0,
  };
  const host: TemplateHost = {
    phoneNav: () => state.phoneNav,
    setPhoneNav: (m) => { state.phoneNav = m; state.writes++; },
    sidebarIcons: () => state.icons,
    setSidebarIcons: (m) => { state.icons = m; state.writes++; },
    modules: () => ["health", "voice"].map((id) => ({ id, title: id, off: state.off.has(id) })),
    setModuleOff: (id, off) => { if (off) state.off.add(id); else state.off.delete(id); state.writes++; },
    startPage: () => state.start,
    saveStartPage: async (k) => { state.start = k; state.writes++; },
    sidebarPrefs: () => state.prefs,
    setSidebarPrefs: (p) => { state.prefs = p ?? NONE; state.writes++; },
    extension: async (id) => state.ext[id] as never,
    setExtensionEnabled: async (id, on) => { state.ext[id].enabled = on; state.writes++; },
    setExtensionConfig: async (id, c) => { state.ext[id].config = c; state.writes++; },
    settings: async (items, dry) => {
      state.sent.push({ items, dry });
      if (o.settings === "missing") return undefined;
      if (o.settings === "fail") throw new Error("hub down");
      return items.map((i) => (i.key === "no.such" ? { key: i.key, status: "unsupported" } : { key: i.key, title: "Title " + i.key, status: dry ? "change" : "applied", text: "before → after" }));
    },
    words: {
      phoneNav: "Phone navigation", phoneNavMode: (m) => m, sidebarIcons: "Sidebar icons", sidebarIconsMode: (m) => m,
      module: (t) => "Module " + t, on: "on", off: "off", startPage: "Start page", startDefault: "main",
      sidebarPages: "Sidebar pages", pagesDefault: "default", pagesUnderMore: (n) => `${n} under More`, pagesCustom: "custom",
      extensionMissing: "not installed", extensionEnabled: "switched", extensionConfig: (k) => "config " + k,
      settingsUnsupportedHub: "old hub", settingStatus: (s) => "status " + s,
    },
  };
  return { host, state };
}

const base = { persona_template: 1, id: "mine", name: "Mine" };

describe("templates: the format", () => {
  it("reads a full template and keeps only what it knows", () => {
    const r = parseTemplate({
      ...base, name: { en: "Mine", "zh-Hans": "我的" }, description: "d",
      browser: { phone_nav: "tabs", sidebar_icons: "colored", modules_off: ["health", "health"] },
      navigation: { start: "agent-list", pages: { hidden: ["todos"], order: [], shown: [] } },
      extensions: { "example-ext": { enabled: true, config: { agent: true } } },
      settings: [{ key: "assistant.main_tasks_quiet", value: false }, { key: "agents.hidden_dirs", action: "add", value: "~/x" }],
    });
    expect("template" in r).toBe(true);
    if (!("template" in r)) return;
    expect(r.template.browser).toEqual({ phone_nav: "tabs", sidebar_icons: "colored", modules_off: ["health"] });
    expect(r.template.navigation?.pages?.hidden).toEqual(["todos"]);
    expect(r.template.settings?.[1]).toEqual({ key: "agents.hidden_dirs", action: "add", value: "~/x" });
    expect(localized(r.template.name, "zh-Hans")).toBe("我的");
    expect(localized(r.template.name, "ja")).toBe("Mine");
  });

  it("refuses what is not a template, with a reason", () => {
    for (const bad of [
      null, [], {}, { ...base, persona_template: 2 }, { ...base, id: "Has Caps" }, { ...base, name: "" },
      { ...base, extra: 1 }, { ...base, browser: { phone_nav: "bottom" } }, { ...base, navigation: { start: "../x" } },
      { ...base, extensions: { "bad id": {} } }, { ...base, extensions: { x: { enabled: "yes" } } },
      { ...base, settings: [{ key: "k" }] }, { ...base, settings: [{ key: "k", value: 1, action: "drop" }] },
      { ...base, settings: Array.from({ length: 65 }, () => ({ key: "k", value: 1 })) },
    ]) {
      const r = parseTemplate(bad);
      expect("error" in r, JSON.stringify(bad)?.slice(0, 60)).toBe(true);
    }
  });

  it("ships Default as the only built-in, and it reads back as itself", () => {
    const builtIns = builtInTemplates((k) => k);
    expect(builtIns.map((b) => b.id)).toEqual(["default"]);
    for (const b of builtIns) expect(parseTemplate(JSON.parse(JSON.stringify(b)))).toEqual({ template: b });
    expect(builtIns[0].browser).toEqual({ phone_nav: "switch", sidebar_icons: "monochrome", modules_off: [] });
    expect(builtIns[0].navigation).toEqual({ pages: null });
  });
});

describe("templates: links", () => {
  const builtIns = builtInTemplates((k) => k);
  const mine = parseTemplate({ ...base, name: "我的模版", browser: { phone_nav: "tabs" } });
  if (!("template" in mine)) throw new Error("fixture");

  it("names a built-in by its id and carries any other template whole", () => {
    expect(templateLink("https://hub.example", builtIns[0], true)).toBe("https://hub.example/settings?tab=general&template=default");
    const link = templateLink("https://hub.example", mine.template, false);
    expect(readTemplateInput(link, builtIns)).toEqual({ template: mine.template, builtIn: false });
    expect(readTemplateInput("default", builtIns)).toEqual({ template: builtIns[0], builtIn: true });
    expect("error" in readTemplateInput("agent-console", builtIns)).toBe(true);
    expect(readTemplateInput(JSON.stringify(mine.template), builtIns)).toEqual({ template: mine.template, builtIn: false });
  });

  it("says why an input is not a template", () => {
    for (const bad of ["", "   ", "{nope", "https://hub.example/settings", "not-a-template", JSON.stringify({ id: "x" })]) {
      expect("error" in readTemplateInput(bad, builtIns), bad).toBe(true);
    }
  });
});

describe("templates: plan and apply", () => {
  const t: PersonaTemplate = {
    persona_template: 1, id: "t", name: "T",
    browser: { phone_nav: "tabs", sidebar_icons: "monochrome", modules_off: ["health"] },
    navigation: { start: "agent-list", pages: { order: [], shown: [], hidden: ["todos", "kb"] } },
    extensions: { "example-ext": { enabled: true, config: { agent: true, agent_prefix: "/agent" } }, missing: { enabled: true } },
    settings: [{ key: "assistant.main_tasks_quiet", value: false }, { key: "no.such", value: 1 }],
  };
  const extensions = { "example-ext": { name: "Example Extension", enabled: false, config: { agent: true, tz: "UTC" } } };

  it("plans every part without writing anything, asking the manager about the settings as a dry run", async () => {
    const { host, state } = fakeHost({ extensions });
    const plan = await planTemplate(t, host);
    expect(state.writes).toBe(0);
    expect(state.sent).toEqual([{ items: t.settings, dry: true }]);
    expect(plan.map((i) => [i.part, i.label, i.status])).toEqual([
      ["browser", "Phone navigation", "change"],
      ["browser", "Sidebar icons", "unchanged"],
      ["browser", "Module health", "change"],
      ["browser", "Module voice", "change"],
      ["navigation", "Start page", "change"],
      ["navigation", "Sidebar pages", "change"],
      ["extensions", "Example Extension", "change"],
      ["extensions", "Example Extension", "change"],
      ["extensions", "missing", "skipped"],
      ["settings", "Title assistant.main_tasks_quiet", "change"],
      ["settings", "no.such", "skipped"],
    ]);
    expect(plan[5].detail).toBe("default → 2 under More");
    expect(plan[10].detail).toBe("status unsupported");
  });

  it("applies every part, keeps an extension's other settings, and finds nothing left to do after", async () => {
    const { host, state } = fakeHost({ extensions });
    const done = await applyTemplate(t, host);
    expect(done.filter((i) => i.status === "applied")).toHaveLength(8);
    expect(state.phoneNav).toBe("tabs");
    expect([...state.off]).toEqual(["health"]);
    expect(state.start).toBe("agent-list");
    expect(state.prefs.hidden).toEqual(["todos", "kb"]);
    expect(state.ext["example-ext"]).toEqual({ name: "Example Extension", enabled: true, config: { agent: true, tz: "UTC", agent_prefix: "/agent" } });
    expect(state.sent.at(-1)?.dry).toBe(false);
    const again = await planTemplate({ ...t, settings: undefined }, host);
    expect(again.filter((i) => i.status === "change")).toEqual([]);
  });

  it("puts the sidebar back for a template that resets it, and skips settings a hub cannot take", async () => {
    const { host, state } = fakeHost({ settings: "missing" });
    state.prefs = { order: [], shown: [], hidden: ["todos"] };
    const done = await applyTemplate({ persona_template: 1, id: "d", name: "D", navigation: { pages: null }, settings: [{ key: "k", value: 1 }] }, host);
    expect(state.prefs).toEqual(NONE);
    expect(done.map((i) => i.status)).toEqual(["applied", "skipped"]);
    expect(done[1].detail).toBe("old hub");
  });

  it("reports a failing part and goes on with the rest", async () => {
    const { host, state } = fakeHost({ settings: "fail" });
    host.saveStartPage = async () => { throw new Error("refused"); };
    const done = await applyTemplate({ persona_template: 1, id: "x", name: "X", browser: { phone_nav: "tabs" }, navigation: { start: "kb" }, settings: [{ key: "k", value: 1 }] }, host);
    expect(done.map((i) => [i.status, i.detail])).toEqual([["applied", "switch → tabs"], ["failed", "refused"], ["failed", "hub down"]]);
    expect(state.phoneNav).toBe("tabs");
  });
});
