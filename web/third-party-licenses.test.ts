// The web build's third-party notices cover every package it can ship: a new dependency fails here, as it fails the
// build, until its license is accepted, its choice of license made, or its missing license file acknowledged
// (third-party-licenses.ts).
import { describe, expect, it } from "vitest";
import { CHOSEN, licenseTexts, NO_LICENSE_FILE, notices, shippedPackages, sourceUrl, UNDECLARED, uncovered, withoutEmails } from "./third-party-licenses";

const packages = shippedPackages();
const named = (name: string) => packages.filter((p) => p.name === name);

describe("third-party licenses of the web build", () => {
  it("covers every package the client ships", () => {
    expect(uncovered(packages)).toEqual([]);
  });

  it("refuses a license not accepted, a choice no longer offered and a missing license file", () => {
    const [dompurify] = named("dompurify");
    const [isarray] = named("isarray");
    expect(uncovered([{ ...dompurify, declared: "GPL-3.0-only", license: "GPL-3.0-only" }])).toEqual([
      `dompurify@${dompurify.version}: GPL-3.0-only is not accepted`,
      `dompurify@${dompurify.version} no longer offers Apache-2.0: GPL-3.0-only`,
    ]);
    expect(uncovered([{ ...isarray, name: "isarray2" }])).toEqual([`isarray2@${isarray.version} is published without a license file`]);
  });

  it("keeps no stale entry in its lists", () => {
    for (const name of [...Object.keys(CHOSEN), ...Object.keys(UNDECLARED), ...NO_LICENSE_FILE]) expect(named(name).length, name).toBeGreaterThan(0);
    for (const name of Object.keys(UNDECLARED)) for (const p of named(name)) expect(p.declared, name).toBeUndefined();
    for (const name of NO_LICENSE_FILE) for (const p of named(name)) expect(licenseTexts(p.dir), name).toEqual([]);
  });

  it("lists every package with an https source and no email address", () => {
    const text = notices(packages);
    for (const p of packages) expect(text).toContain(`\n${p.name} ${p.version}\nLicense: ${p.license}`);
    for (const [name, license] of Object.entries(CHOSEN)) expect(text).toMatch(new RegExp(`\\n${name} \\S+\\nLicense: ${license} \\(offered: `));
    expect(text).not.toMatch(/^Source: (?!https:\/\/)/m);
    expect(text.match(/[\w.%+-]+@[\w-]+(?:\.[\w-]+)*\.[A-Za-z]{2,}/g)).toBeNull();
  });

  it("leaves out email addresses and keeps the rest of the line", () => {
    expect(withoutEmails("Copyright (c) 2015 A B <a@example.com>")).toBe("Copyright (c) 2015 A B");
    expect(withoutEmails("Copyright (c) A B <mailto:a@example.com>")).toBe("Copyright (c) A B");
    expect(withoutEmails("Copyright (c) A B <a@example.com> (example.com)")).toBe("Copyright (c) A B (example.com)");
    expect(withoutEmails("Copyright (c) A B (a@example.com)")).toBe("Copyright (c) A B");
    expect(withoutEmails("Copyright (c) 2013 A B (a@example.com, https://example.com/ab)")).toBe("Copyright (c) 2013 A B (https://example.com/ab)");
    expect(withoutEmails("Write to a@example.com.")).toBe("Write to.");
  });

  it("turns package.json repository forms into browsable URLs", () => {
    expect(sourceUrl("git+ssh://git@example.com/a/b.git", "b", "1.0.0")).toBe("https://example.com/a/b");
    expect(sourceUrl({ type: "git", url: "git://github.com/a/b.git" }, "b", "1.0.0")).toBe("https://github.com/a/b");
    expect(sourceUrl("git@example.com:a/b.git", "b", "1.0.0")).toBe("https://example.com/a/b");
    expect(sourceUrl("http://github.com/a/b", "b", "1.0.0")).toBe("https://github.com/a/b");
    expect(sourceUrl("github:a/b", "b", "1.0.0")).toBe("https://github.com/a/b");
    expect(sourceUrl("a/b", "b", "1.0.0")).toBe("https://github.com/a/b");
    expect(sourceUrl(undefined, "@s/b", "2.0.0")).toBe("https://www.npmjs.com/package/@s/b/v/2.0.0");
  });
});
