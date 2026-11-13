import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { BOOTSTRAP_TAG, inlineBootstrap } from "./compat-bootstrap";
import config from "./vite.config";

const read = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");
const code = read("./public/compatibility-bootstrap.js");

describe("compat-bootstrap", () => {
  for (const [name, path] of [["the client page", "./index.html"], ["the admin page", "./admin/index.html"]]) {
    it(`puts the legacy theme copy inline in ${name}, still ahead of its theme script`, () => {
      const html = inlineBootstrap(read(path), code);
      expect(html).not.toContain("compatibility-bootstrap.js");
      // no classic script left that the page has to fetch before its first paint
      expect(html).not.toMatch(/<script(?![^>]*(?:type="module"|\basync\b|\bdefer\b))[^>]*\ssrc=/);
      const inline = html.indexOf(code.trim());
      expect(inline).toBeGreaterThan(0);
      expect(inline).toBeLessThan(html.indexOf('localStorage.getItem("persona.theme") || "system"'));
    });
  }

  it("is one of the build's plugins", () => {
    const names = (config.plugins ?? []).flat().map((p) => (p && typeof p === "object" && "name" in p ? p.name : ""));
    expect(names).toContain("compat-bootstrap");
  });

  it("fails the build when a page no longer has the tag, rather than shipping it without the copy", () => {
    expect(() => inlineBootstrap("<head></head>", code, "x.html")).toThrow(`x.html has no ${BOOTSTRAP_TAG}`);
  });

  it("refuses a script that would end the inline tag early", () => {
    expect(() => inlineBootstrap(`<head>${BOOTSTRAP_TAG}</head>`, "a('</script>')")).toThrow("</script");
  });
});
