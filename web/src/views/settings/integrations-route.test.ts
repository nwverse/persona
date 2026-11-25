// Unit tests for Settings › Integrations route codec, including `-sources` and `-sources/<encodedState>` (#15423, #15424).
import { describe, expect, it } from "vitest";
import {
  formatIntegrationsSub,
  integrationsToLegacy,
  isIntegrationDetailRoute,
  isLegacyIntegrationTab,
  legacyToIntegrations,
  parseIntegrationsSub,
  parseSettingsPath,
  type IntegrationsRoute,
} from "./integrations-route";

describe("integrations-route sources and data-flow codec", () => {
  it("treats sources alongside channels/connectors/extensions as a legacy integration tab", () => {
    for (const tab of ["channels", "connectors", "extensions", "sources"]) {
      expect(isLegacyIntegrationTab(tab)).toBe(true);
    }
    expect(isLegacyIntegrationTab("integrations")).toBe(false);
    expect(isLegacyIntegrationTab("general")).toBe(false);
  });

  it("parses and formats -sources and -sources/<encodedState> with optional @manager", () => {
    const sampleState = JSON.stringify({ p: "source", id: "sub_push_1" });
    const cases: Array<{ route: IntegrationsRoute; sub: string }> = [
      { route: { kind: "sources" }, sub: "-sources" },
      { route: { kind: "sources", manager: "work" }, sub: "-sources@work" },
      {
        route: { kind: "sources", state: sampleState },
        sub: `-sources/${encodeURIComponent(sampleState)}`,
      },
      {
        route: { kind: "sources", state: sampleState, manager: "work" },
        sub: `-sources@work/${encodeURIComponent(sampleState)}`,
      },
    ];

    for (const { route, sub } of cases) {
      expect(formatIntegrationsSub(route)).toBe(sub);
      expect(parseIntegrationsSub(sub)).toEqual(route);
    }

    // Also handles already-decoded JSON state (e.g. from parseSettingsPath).
    expect(parseIntegrationsSub(`-sources/${sampleState}`)).toEqual({ kind: "sources", state: sampleState });
    expect(parseIntegrationsSub(`-sources@work/${sampleState}`)).toEqual({
      kind: "sources",
      state: sampleState,
      manager: "work",
    });
  });

  it("maps legacy sources routes into -sources with preserved @manager and encoded state", () => {
    const sampleState = JSON.stringify({ p: "source", id: "sub_1" });
    const encoded = encodeURIComponent(sampleState);
    expect(legacyToIntegrations("sources", null)).toEqual({ sub: "-sources" });
    expect(legacyToIntegrations("sources", null, "work")).toEqual({ sub: "-sources@work" });
    expect(legacyToIntegrations("sources", sampleState)).toEqual({ sub: `-sources/${encoded}` });
    expect(legacyToIntegrations("sources", sampleState, "work")).toEqual({ sub: `-sources@work/${encoded}` });
    expect(legacyToIntegrations("sources", `@work/${sampleState}`)).toEqual({ sub: `-sources@work/${encoded}` });
  });

  it("keeps global Data Flow out of split-pane detail mode while allowing single-source details in split-pane", () => {
    expect(isIntegrationDetailRoute({ kind: "sources" })).toBe(false);
    expect(isIntegrationDetailRoute({ kind: "sources", manager: "work" })).toBe(false);
    expect(isIntegrationDetailRoute({ kind: "sources", state: '{"p":"source","id":"sub_1"}' })).toBe(true);
    expect(isIntegrationDetailRoute({ kind: "sources", state: '{"p":"source","id":"sub_1"}', manager: "work" })).toBe(true);
  });

  it("maps -sources back to legacy Settings › Inbox Sources while preserving manager and state", () => {
    const sampleState = JSON.stringify({ p: "source", id: "sub_1" });
    const encoded = encodeURIComponent(sampleState);
    expect(integrationsToLegacy("-sources")).toEqual({ tab: "sources", sub: null });
    expect(integrationsToLegacy("-sources@work")).toEqual({ tab: "sources", sub: null, manager: "work" });
    expect(integrationsToLegacy(`-sources/${encoded}`)).toEqual({
      tab: "sources",
      sub: sampleState,
    });
    expect(integrationsToLegacy(`-sources@work/${encoded}`)).toEqual({
      tab: "sources",
      sub: sampleState,
      manager: "work",
    });
  });

  it("parses path-based /settings/sources URLs including state and @manager", () => {
    const sampleState = JSON.stringify({ p: "source", id: "sub_1" });
    const encoded = encodeURIComponent(sampleState);
    expect(parseSettingsPath("/settings/sources")).toEqual({ tab: "sources", sub: null });
    expect(parseSettingsPath("/settings/sources/")).toEqual({ tab: "sources", sub: null });
    expect(parseSettingsPath(`/settings/sources/${encoded}`)).toEqual({ tab: "sources", sub: sampleState });
    expect(parseSettingsPath("/settings/sources@work")).toEqual({ tab: "sources", sub: null, manager: "work" });
    expect(parseSettingsPath(`/settings/sources@work/${encoded}`)).toEqual({
      tab: "sources",
      sub: sampleState,
      manager: "work",
    });
  });
});
