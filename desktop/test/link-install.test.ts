import assert from "node:assert/strict";
import { join } from "node:path";
import test from "node:test";
import { personaLinkExecutable } from "../src/link.js";

test("the connector is a fixed separate installation, not a renderer-chosen executable", () => {
  // The same fixed place on every platform, written the way that platform writes a path and names a program.
  const program = process.platform === "win32" ? "persona-link.exe" : "persona-link";
  assert.equal(personaLinkExecutable("/home/example"), join("/home/example", ".persona", "bin", program));
  assert.match(personaLinkExecutable("/home/example"), /[\\/]home[\\/]example[\\/]\.persona[\\/]bin[\\/]persona-link(\.exe)?$/);
});
