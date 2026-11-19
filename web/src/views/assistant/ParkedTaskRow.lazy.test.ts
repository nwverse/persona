/// <reference types="node" />
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import startup from "../../i18n/startup-namespaces.json";
import tasks from "../../i18n/locales/en/assistant-tasks.json";
import details from "../../i18n/locales/en/assistant-task-details.json";
import { startupMatcher } from "../../i18n/startup-keys";

describe("wait information stays off the first screen", () => {
  it("loads an unfinished stop's expanded body with the existing Needs you details", () => {
    const entry = readFileSync(new URL("./Tasks.tsx", import.meta.url), "utf8");
    const details = readFileSync(new URL("./TaskDecision.tsx", import.meta.url), "utf8");
    expect(entry).toContain('lazyPage(() => import("./TaskDecision").then((m) => m.WaitingTaskRow))');
    expect(entry).not.toContain("function WaitingTaskRow(");
    expect(details).toContain("export function WaitingTaskRow(");
    expect(details).toContain('data-needs-reason={unattended ? "stopped_unfinished"');
  });

  it("keeps the failed and waiting rows' labels in their shared lazy details", () => {
    const entry = readFileSync(new URL("./Tasks.tsx", import.meta.url), "utf8");
    const failed = readFileSync(new URL("./FailedTaskRow.tsx", import.meta.url), "utf8");
    expect(entry).not.toContain("function waitsLabel(");
    expect(entry).not.toContain("const ROW_ACTIONS");
    expect(failed).toContain("export function waitsLabel(");
    expect(failed).toContain("const ROW_ACTIONS");
  });

  it("loads task details' unchanged words after the first screen", () => {
    expect(Object.keys(details)).toEqual(expect.arrayContaining(["workspace_engine_path", "plan_edit_model_placeholder_kept", "tray_unattended"]));
    const inStartup = startupMatcher(startup);
    for (const key of Object.keys(details)) {
      expect(tasks).not.toHaveProperty(key);
      expect(inStartup(`assistant-task-details.${key}`)).toBe(false);
    }
  });

  it("loads progress and acceptance rows only after opening In progress", () => {
    const entry = readFileSync(new URL("./Tasks.tsx", import.meta.url), "utf8");
    const details = readFileSync(new URL("./TaskDecision.tsx", import.meta.url), "utf8");
    expect(entry).toContain('lazyPage(() => import("./TaskDecision").then((m) => m.ProgressTaskRow))');
    expect(entry).not.toContain("function TaskProgressMeter(");
    expect(details).toContain("export function ProgressTaskRow(");
    expect(details).toContain("function TaskProgressMeter(");
    expect(details).toContain("tasks.BackgroundWorkDetail");
    expect(details).toContain('data-task-status={isNew ? "just_finished" : rowIsFinished ? "done" : agentKey}');
  });

  it("keeps all three workspace summary modes available before details load", () => {
    const inStartup = startupMatcher(startup);
    const controls = readFileSync(new URL("./TaskWorkspaceControls.tsx", import.meta.url), "utf8");
    for (const language of ["en", "ja", "zh-Hans"]) {
      const words = JSON.parse(readFileSync(
        new URL(`../../i18n/locales/${language}/assistant-tasks.json`, import.meta.url), "utf8",
      )) as Record<string, string>;
      for (const mode of ["new", "existing", "none"]) {
        expect(words[`workspace_${mode}`], `${language}.${mode}`).toBeTruthy();
        expect(inStartup(`assistant-tasks.workspace_${mode}`)).toBe(true);
        expect(controls).toContain(`t("assistant-tasks.workspace_${mode}")`);
      }
    }
  });

  it("loads wait ordering and new detail copy only with the waiting list", () => {
    const source = readFileSync(new URL("./Tasks.tsx", import.meta.url), "utf8");
    expect(source).not.toMatch(/^import[^;]*from\s+["']\.\/ParkedTaskRow["']/m);
    expect(source).toContain('lazyPage(() => import("./ParkedTaskRow").then((m) => m.ParkedTaskRows))');
    for (const name of ["assistant-entries", "todo-triage"]) {
      const shared = readFileSync(new URL(`../../lib/${name}.ts`, import.meta.url), "utf8");
      expect(shared).not.toContain('from "./task-wait"');
    }
    const inStartup = startupMatcher(startup);
    expect(inStartup("task-wait.last_contact")).toBe(false);
    expect(inStartup("assistant-tasks.progress_count")).toBe(true);
  });
});
