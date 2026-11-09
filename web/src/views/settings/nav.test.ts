import { describe, expect, it } from "vitest";
import { FROM_LIST, LIST_GROUPS, openedFromList, settingsAt, settingsHref, TAB_INFO, TABS } from "./nav";

describe("settings location", () => {
  it("names no category on plain /settings: the phone's list (通用 on a wide screen)", () => {
    expect(settingsAt("")).toEqual({ tab: null, sub: null });
    expect(settingsAt("?tab=nope")).toEqual({ tab: null, sub: null });
  });
  it("reads the category and the item its page has open", () => {
    expect(settingsAt("?tab=codex")).toEqual({ tab: "codex", sub: null });
    expect(settingsAt("?tab=model-config&purpose=agent.triage")).toEqual({ tab: "model-config", sub: "agent.triage" });
    expect(settingsAt("?tab=codex&purpose=x")).toEqual({ tab: "codex", sub: null });
  });
  it("names 通用 on a phone, where /settings is the list, and keeps /settings for it on a wide screen", () => {
    expect(settingsHref("general", null, false)).toBe("/settings");
    expect(settingsHref("general", null, true)).toBe("/settings?tab=general");
    expect(settingsHref("channels", "wechat", false)).toBe("/settings?tab=channels&channel=wechat");
    expect(settingsHref("hub", "ignored", true)).toBe("/settings?tab=hub");
  });
  it("preserves an explicit assistant owner in configuration links", () => {
    expect(settingsHref("model-config", "agent.new", false, "work")).toBe("/settings?tab=model-config&purpose=agent.new&manager=work");
    expect(settingsHref("assistant", null, false, "assistant")).toBe("/settings?tab=assistant&manager=assistant");
  });
  it("round-trips every category through its URL", () => {
    for (const t of TABS) expect(settingsAt(settingsHref(t, null, true).split("?")[1] ?? "").tab).toBe(t);
  });
  it("lists each category once on the phone's first page", () => {
    const listed = LIST_GROUPS.flatMap((g) => g.tabs);
    expect([...listed].sort()).toEqual([...TABS].sort());
  });
  it("knows a category was opened from the list (Back goes to it)", () => {
    expect(openedFromList({ [FROM_LIST]: true, personaOverlay: "o1" })).toBe(true);
    expect(openedFromList(null)).toBe(false);
    expect(openedFromList({ personaOverlay: "o1" })).toBe(false);
  });
  it("opens a link from before the rename (?tab=plugins&plugin=…) as 扩展", () => {
    expect(settingsAt("?tab=plugins&plugin=wechat")).toEqual({ tab: "extensions", sub: "wechat" });
    expect(settingsAt("?tab=extensions&extension=x")).toEqual({ tab: "extensions", sub: "x" });
  });
  it("opens legacy channel links in the independent channel category", () => {
    expect(settingsAt("?tab=plugins&plugin=-channels%2Fwechat")).toEqual({ tab: "channels", sub: "wechat" });
    expect(settingsAt("?tab=extensions&extension=-channels%2Fwechat")).toEqual({ tab: "channels", sub: "wechat" });
    expect(settingsAt("?tab=extensions&extension=-channels")).toEqual({ tab: "channels", sub: null });
    expect(settingsAt("?tab=channels&channel=wechat")).toEqual({ tab: "channels", sub: "wechat" });
  });
  it("puts channels immediately before connectors in both layouts", () => {
    expect(TABS[TABS.indexOf("connectors") - 1]).toBe("channels");
    const listed = LIST_GROUPS.flatMap((g) => g.tabs);
    expect(listed[listed.indexOf("connectors") - 1]).toBe("channels");
  });
  it("gives every category a one-line hint, and no group shares a name with one of its rows (#3636)", () => {
    for (const k of TABS) expect(TAB_INFO[k].hint.trim()).not.toBe("");
    for (const g of LIST_GROUPS) for (const k of g.tabs) expect(TAB_INFO[k].label).not.toBe(g.title);
  });
});
