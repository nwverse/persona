// The route table every client follows (docs/protocol/deep-links.md, #9926): the shared cases of
// spec/fixtures/deep-links/cases.json read the same here as on the iPhone (HubPageLinkTests) and Android. The web is one
// hub's page, so it reads the cases whose only paired hub is its own (that hub's addresses are its origins).
import { describe, expect, it } from "vitest";
import fixture from "../../../spec/fixtures/deep-links/cases.json";
import { appPage, DEEP_KB_KINDS, DEEP_SETTINGS_TABS, pageOfPath, pagePath, webPage } from "./deep-links";
import { KB_KINDS } from "./kb";
import { parse as parseRoute } from "./router";
import { TABS } from "../views/settings/nav";

interface Hub {
  id: string;
  addresses: string[];
}
interface ParseCase {
  name: string;
  url: string;
  hubs: string[];
  page: Record<string, string> | null;
  reason?: string;
}
const hubs = fixture.hubs as Record<string, Hub>;
const cases = fixture.parse as ParseCase[];

/** What this page opens for a link when `hub` is the hub it is served by, as the fixture writes a page. */
function opened(url: string, hub: Hub): Record<string, string> | null {
  let u: URL;
  try {
    u = new URL(url);
  } catch {
    return null;
  }
  const app = appPage(u);
  if (app) return { ...app.page, ...(app.hub ? { hub: app.hub } : {}) } as Record<string, string>;
  // each address is an origin, and the sub-path the hub is served at (lib/base.ts) is its path
  for (const a of hub.addresses) {
    const base = new URL(a);
    const page = webPage(u, [base.origin], base.pathname.replace(/\/$/, ""));
    if (page) return { ...page, hub: hub.id } as Record<string, string>;
  }
  return null;
}

describe("deep links: the shared cases", () => {
  it("has the cases", () => {
    expect(fixture.version).toBe(1);
    expect(cases.length).toBeGreaterThan(60);
  });
  for (const c of cases.filter((c) => c.hubs.length === 1)) {
    it(c.name, () => {
      expect(opened(c.url, hubs[c.hubs[0]!]!)).toEqual(c.page);
      expect(c.page === null).toBe(c.reason !== undefined);
    });
  }
});

describe("deep links: pages and paths", () => {
  it("every page's path reads back as the same page, and the router opens it", () => {
    for (const c of cases) {
      if (!c.page) continue;
      const { hub: _hub, ...page } = c.page;
      void _hub;
      const path = pagePath(page as never);
      const [p, q = ""] = path.split("?");
      expect(pageOfPath(p!, q ? `?${q}` : ""), `${c.name}: ${path}`).toEqual(page);
      expect(parseRoute(p!, q).name, path).not.toBe("not-found");
    }
  });

  it("keeps the knowledge kinds and settings tabs of the pages that define them", () => {
    expect(DEEP_KB_KINDS).toEqual([...KB_KINDS]);
    expect(Object.keys(DEEP_SETTINGS_TABS).sort()).toEqual([...TABS].sort());
  });
});
