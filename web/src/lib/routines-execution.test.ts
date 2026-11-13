import { expect, it } from "vitest";
import { blankTask, sameTask, taskBody } from "./routines";

it("saves task execution edits and clears overrides explicitly", () => {
  const saved = { ...blankTask(), id: "task", launcher: "codex", model: "test-model", cwd: "/work/project", execution_machine_id: "remote", model_revision: 3 };
  const draft = { ...saved, model: "another-model", cwd: "/work/other" };
  expect(sameTask(saved, draft)).toBe(false);
  expect(taskBody(draft, saved)).toMatchObject({ model: "another-model", cwd: "/work/other", model_revision: 3 });
  expect(taskBody({ ...saved, launcher: "", model: "", cwd: "", execution_machine_id: "" }, saved)).toMatchObject({ launcher: "", model: "", cwd: "", execution_machine_id: "" });
  expect(taskBody({ ...saved, cwd: "/work/project " }, saved).cwd).toBe("/work/project ");
  expect(sameTask(saved, { ...saved, cwd: "/work/project " })).toBe(false);
  expect(taskBody(saved, saved)).not.toHaveProperty("model");
  expect(taskBody(blankTask())).not.toHaveProperty("cwd");
});
