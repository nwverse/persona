// Optional task ownership must not increase the first-screen catalog budget.
import { readFileSync } from "node:fs";
import { expect, it } from "vitest";

it("keeps the closed-task notice and its copy outside the first screen", async () => {
  const source = readFileSync(new URL("./ChatView.tsx", import.meta.url), "utf8");
  const namespaces = JSON.parse(readFileSync(new URL("../../i18n/startup-namespaces.json", import.meta.url), "utf8"));
  expect(source).toMatch(/lazy\(\(\) => import\("\.\/ClosedTaskNotice"\)\)/);
  expect(source).not.toMatch(/^import[^;]*from\s+["']\.\/ClosedTaskNotice["']/m);
  expect(namespaces).not.toContain("chat-closed-task");
  expect(typeof (await import("./ClosedTaskNotice")).default).toBe("function");
});
