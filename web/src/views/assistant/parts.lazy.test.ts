/// <reference types="node" />
// Home (Tag) and Add a computer (Empty) are first-screen files. Taking those two from views/assistant/parts.tsx put the
// whole file, the settings pages' fields, switches and segmented controls, on every first screen; they live in
// ui/atoms.tsx, which is there anyway.
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import * as atoms from "../../ui/atoms";
import * as parts from "./parts";

const source = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");

describe("views/assistant/parts.tsx", () => {
  it("stays off the first screen: Home and Add a computer take Tag and Empty from ui/atoms", () => {
    expect(source("../Home.tsx")).not.toMatch(/from\s+["']\.\/assistant\/parts["']/);
    expect(source("../machines/AddComputerEntry.tsx")).not.toMatch(/from\s+["']\.\.\/assistant\/parts["']/);
  });

  it("re-exports the same Tag and Empty for its other users", () => {
    expect(parts.Tag).toBe(atoms.Tag);
    expect(parts.Empty).toBe(atoms.Empty);
  });
});
