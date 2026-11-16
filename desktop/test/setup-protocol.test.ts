import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { SETUP_URL, setupResourceURL } from "../src/setup-resources.js";

const allowed = (raw: string) => {
  const resource = setupResourceURL(raw);
  assert.ok(resource);
  return new URL(resource).pathname;
};

test("the trusted setup protocol serves exactly its three public resources", () => {
  assert.match(allowed(SETUP_URL), /\/setup\/setup\.html$/);
  assert.match(allowed("persona-setup://app/setup.css"), /\/setup\/setup\.css$/);
  assert.match(allowed("persona-setup://app/setup.cjs"), /\/setup\/setup\.cjs$/);
});

test("the development driver identifies only the trusted local setup origin", () => {
  const source = readFileSync(new URL("../../scripts/e2e-main.mjs", import.meta.url), "utf8");
  assert.match(source, /const isLocalSettingsURL = url => url === "persona-setup:\/\/app\/setup\.html"/);
  assert.doesNotMatch(source, /url\.endsWith\("\/setup\.html"\)/);
});

test("no renderer-controlled path can reach another asar resource", () => {
  const rejected = [
    "persona-setup://app/setup-preload.cjs",
    "persona-setup://app/locales/en.json",
    "persona-setup://app/../main.js",
    "persona-setup://app/%2e%2e/main.js",
    "persona-setup://app/%2Fmain.js",
    "persona-setup://app/..%2fmain.js",
    "persona-setup://app/..%5cmain.js",
    "persona-setup://other/setup.html",
    "persona-setup://app:444/setup.html",
    "persona-setup://sub.app/setup.html",
    "persona-setup://user@app/setup.html",
    "persona-setup://app/setup.html?file=main.js",
    "persona-setup://app/setup.html#fragment",
    "file:///tmp/setup.html",
    "persona-setup.evil://app/setup.html",
  ];
  for (const raw of rejected) assert.equal(setupResourceURL(raw), undefined, raw);
  assert.equal(setupResourceURL(SETUP_URL, "POST"), undefined);
  assert.equal(setupResourceURL(SETUP_URL, "DELETE"), undefined);
});
