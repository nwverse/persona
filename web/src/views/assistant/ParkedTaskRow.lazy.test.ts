/// <reference types="node" />
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import startup from "../../i18n/startup-namespaces.json";
import { startupMatcher } from "../../i18n/startup-keys";

describe("wait information stays off the first screen", () => {
  it("loads the waiting row on demand and defers its detail and PR-blocker copy", () => {
    const source = readFileSync(new URL("./Tasks.tsx", import.meta.url), "utf8");
    expect(source).not.toMatch(/^import[^;]*from\s+["']\.\/ParkedTaskRow["']/m);
    expect(source).toContain('lazyPage(() => import("./ParkedTaskRow").then((m) => m.ParkedTaskRow))');
    const inStartup = startupMatcher(startup);
    expect(inStartup("task-wait.last_contact")).toBe(false);
    expect(inStartup("assistant-tasks.pr_blocker_review_required")).toBe(false);
    expect(inStartup("assistant-tasks.progress_count")).toBe(true);
    expect(inStartup("assistant-tasks.prepare")).toBe(true);
  });
});
