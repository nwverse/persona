// @vitest-environment jsdom
// The plan card's "Start these directly" says what it remembers (#8095): each task that changes files, by its computer
// and folder, with the folder's subfolders; read-only tasks (they start without asking anyway) are not named, and a plan
// of only those offers nothing to remember.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it } from "vitest";
import type { PlanTask, TaskPlan } from "../../lib/feed";

const { getState } = await import("../../lib/store");
const { formatList } = await import("../../i18n");
const { PlanCard, rememberScope } = await import("./Tasks");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
const seed = (patch: Record<string, unknown>) => Object.assign(getState() as unknown as Record<string, unknown>, patch);
const before = { ...getState() };
const task = (o: Partial<PlanTask> = {}): PlanTask => ({ title: "补测试", prompt: "补", machine: "m1", cwd: "/home/me/src/tool", ...o });
const plan = (tasks: PlanTask[], start?: TaskPlan["start"]): TaskPlan => ({ id: "p1", conversation: "c1", state: "open", tasks, ...(start ? { start } : {}) });
const machines = [{ id: "m1", name: "工作机" }, { id: "m2", name: "家里" }] as never;

beforeEach(() => {
  seed({ machines });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  seed(before);
});

it("names each writable task's computer and folder once, with its subfolders", () => {
  const line = rememberScope([task(), task({ title: "又一个" }), task({ machine: "m2", cwd: "/opt/app" }), task({ cwd: "/docs", readonly: true })], machines);
  expect(line).toBe(`今后在${formatList(["「工作机」的 /home/me/src/tool", "「家里」的 /opt/app"])} 及其子目录中修改文件的任务将直接启动`);
  expect(line).not.toContain("/docs");
});

it("offers nothing to remember when every task only reads (those start without asking anyway)", async () => {
  expect(rememberScope([task({ readonly: true })], machines)).toBe("");
  await act(async () => root.render(<PlanCard plan={plan([task({ readonly: true })])} />));
  expect(host.querySelector("input[type=checkbox]")).toBeNull();
});

it("draws the scope next to the card's checkbox", async () => {
  await act(async () => root.render(<PlanCard plan={plan([task({ cwd: "/home/me/src/tool/cli" })])} />));
  const label = [...host.querySelectorAll("label")].find((l) => l.querySelector("input[type=checkbox]"));
  expect(label?.textContent).toBe("今后在「工作机」的 /home/me/src/tool/cli 及其子目录中修改文件的任务将直接启动");
});

it("on a current hub names only the server's uncovered task indexes", async () => {
  await act(async () => root.render(<PlanCard plan={plan([
    task(), task({ title: "另一个", machine: "m2", cwd: "/opt/app" }), task({ title: "只读", cwd: "/docs", readonly: true }),
  ], { reason: "start_authorization", remember: [1] })} />));
  const label = [...host.querySelectorAll("label")].find((l) => l.querySelector("input[type=checkbox]"));
  expect(label?.textContent).toContain("/opt/app");
  expect(label?.textContent).not.toContain("/home/me/src/tool");
  expect(label?.textContent).not.toContain("/docs");
});

it("on a current hub offers nothing when every writable task is already covered", async () => {
  await act(async () => root.render(<PlanCard plan={plan([task()], { reason: "start_authorization" })} />));
  expect(host.querySelector("input[type=checkbox]")).toBeNull();
});

it("offers nothing for a hard gate or a reason this client does not know", async () => {
  for (const reason of ["owner_step", "request_gate", "continuation", "outbound_confirmation"]) {
    await act(async () => root.render(<PlanCard plan={plan([task()], { reason: reason as "owner_step", remember: [0] })} />));
    expect(host.querySelector("input[type=checkbox]"), reason).toBeNull();
  }
  const future = { reason: "future_gate", remember: [0] } as unknown as TaskPlan["start"];
  await act(async () => root.render(<PlanCard plan={plan([task()], future)} />));
  expect(host.querySelector("input[type=checkbox]"), "unknown reason").toBeNull();
});
