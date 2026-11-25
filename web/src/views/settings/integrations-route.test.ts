// Unit tests for Settings › Integrations route codec, including `-sources` and `-sources/<encodedState>` (#15423).
import { describe, expect, it } from "vitest";
import {
  formatIntegrationsSub,
  integrationsToLegacy,
  isIntegrationDetailRoute,
  parseIntegrationsSub,
  type IntegrationsRoute,
} from "./integrations-route";

describe("integrations-route sources and data-flow codec", () => {
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

  it("keeps global Data Flow out of split-pane detail mode while allowing single-source details in split-pane", () => {
    expect(isIntegrationDetailRoute({ kind: "sources" })).toBe(false);
    expect(isIntegrationDetailRoute({ kind: "sources", manager: "work" })).toBe(false);
    expect(isIntegrationDetailRoute({ kind: "sources", state: '{"p":"source","id":"sub_1"}' })).toBe(true);
    expect(isIntegrationDetailRoute({ kind: "sources", state: '{"p":"source","id":"sub_1"}', manager: "work" })).toBe(true);
  });

  it("maps -sources back to legacy Settings › Inbox Sources when integrations_v2 is turned off", () => {
    expect(integrationsToLegacy("-sources")).toEqual({ tab: "sources", sub: null });
    expect(integrationsToLegacy("-sources@work")).toEqual({ tab: "sources", sub: null });
    expect(integrationsToLegacy(`-sources/${encodeURIComponent('{"p":"source","id":"sub_1"}')}`)).toEqual({
      tab: "sources",
      sub: null,
    });
  });
});
