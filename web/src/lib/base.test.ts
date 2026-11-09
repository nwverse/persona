import { describe, expect, it } from "vitest";
import { baseOf, stripBase } from "./base";
import { href, parse, routePath, type Route } from "./router";
import { linkHref, linkRoute } from "./links";

describe("baseOf", () => {
  it("is empty at the root", () => {
    expect(baseOf("/")).toBe("");
    expect(baseOf(null)).toBe("");
    expect(baseOf("")).toBe("");
  });
  it("is the sub-path without its trailing slash", () => {
    expect(baseOf("/persona/")).toBe("/persona");
    expect(baseOf("/a/b/")).toBe("/a/b");
  });
  it("uses only the pathname of an absolute URL and removes every trailing slash", () => {
    expect(baseOf("https://hub.example.com/a/b///?view=chat#latest")).toBe("/a/b");
    expect(baseOf("https://hub.example.com/?view=chat#latest")).toBe("");
  });
  it("falls back to the root for an invalid URL", () => {
    expect(baseOf("https://[invalid")).toBe("");
    expect(baseOf(undefined)).toBe("");
  });
});

describe("stripBase", () => {
  it("leaves paths alone at the root", () => {
    expect(stripBase("/s/laptop/abc", "")).toBe("/s/laptop/abc");
  });
  it("takes the sub-path off", () => {
    expect(stripBase("/persona/s/laptop/abc", "/persona")).toBe("/s/laptop/abc");
    expect(stripBase("/persona", "/persona")).toBe("/");
    expect(stripBase("/persona/", "/persona")).toBe("/");
  });
  it("does not take a mere prefix of a segment", () => {
    expect(stripBase("/personax/persona", "/persona")).toBe("/personax/persona");
  });
});

describe("routes under a sub-path", () => {
  const routes: Route[] = [
    { name: "persona" },
    { name: "persona", sid: "c 1" },
    { name: "session", mid: "laptop", sid: "abc", view: "term" },
    { name: "tab", mid: "laptop", space: "w1", slot: "2", sid: "abc" },
    { name: "inbox", id: "lark:feed:1" },
    { name: "machines" },
    { name: "machine", mid: "laptop" },
    { name: "terminal", mid: "laptop", id: "pane:w1:p2" },
    { name: "settings" },
    { name: "extension", extension: "a b", ui: "c/d" },
  ];
  it("writes the sub-path in front and reads it back without", () => {
    for (const r of routes) {
      const at = href(r, "/persona");
      expect(at.startsWith("/persona/"), at).toBe(true);
      expect(at).toBe("/persona" + routePath(r));
      const [path, query = ""] = at.split("?");
      expect(parse(stripBase(path!, "/persona"), query)).toEqual(r);
    }
  });
  it("is the root's at the root", () => {
    for (const r of routes) expect(href(r, "")).toBe(routePath(r));
    expect(href({ name: "persona" }, "/persona")).toBe("/persona/");
  });
});

describe("links to this hub's pages under a sub-path", () => {
  const origin = "https://hub.example.com";
  it("opens them in the app", () => {
    expect(linkRoute(`${origin}/persona/s/laptop/abc`, false, origin, "/persona")).toEqual({ kind: "agent", mid: "laptop", sid: "abc" });
    expect(linkRoute(`${origin}/persona/todos?id=t1`, false, origin, "/persona")).toEqual({ kind: "task", id: "t1" });
  });
  it("leaves the rest of the origin to the web", () => {
    expect(linkRoute(`${origin}/s/laptop/abc`, false, origin, "/persona").kind).toBe("web");
    expect(linkRoute(`${origin}/other/s/laptop/abc`, false, origin, "/persona").kind).toBe("web");
  });
  it("keeps persona:// session links in-app at the root", () => {
    expect(linkHref("persona://session/laptop/abc")).toBe("/s/laptop/abc");
  });
});
