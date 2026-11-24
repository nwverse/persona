// @vitest-environment jsdom
// Session Overview Subagents section (#14323, DESIGN_SYSTEM "Session overview · Subagents"):
// unified 3-class aggregation, Running/Ended grouping with default Ended collapse behind up to 4 stacked avatars,
// progressive provenance chips, 2-level nesting cap, and clicking to open kind:"subagent" panel.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { closePanel, getPanel } from "../../lib/panel";
import { getState, reset, upsertSession } from "../../lib/store";
import { emptyTaskIndex } from "../../lib/tasks";
import type { Interaction, Machine, Session } from "../../lib/types";
import { SessionOverview } from "./SessionOverview";
import { subagentProvenance } from "./overview-subagents";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia ??= () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  g.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} };
});

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const machines: Machine[] = [
  { id: "laptop", name: "Laptop", online: true },
  { id: "workstation", name: "Workstation", online: true },
];

const parent: Session = {
  machine_id: "laptop",
  session_id: "p1",
  title: "Ship cross-machine overview",
  cwd: "/code/app",
  agent: "claude",
  status: "working",
  state: "working",
  subagents: [
    {
      id: "emb-run",
      session_id: "p1@emb-run",
      description: "Embedded running agent",
      running: true,
      tool_use_id: "call-emb-1",
      started_at: "2026-11-24T01:05:00Z",
      last_tool: { name: "Bash", summary: "Running vitest suite" },
    },
    {
      id: "emb-done",
      session_id: "p1@emb-done",
      description: "Embedded finished agent",
      running: false,
      tool_use_id: "call-emb-2",
      started_at: "2026-11-24T00:30:00Z",
      updated_at: "2026-11-24T00:35:00Z",
    },
  ],
};

const childSession = (extra: Partial<Session>): Session => ({
  machine_id: "laptop",
  session_id: "c1",
  title: "Child task",
  cwd: "/code/app",
  agent: "codex",
  status: "idle",
  state: "idle",
  started_at: "2026-11-24T01:00:00Z",
  updated_at: "2026-11-24T01:02:00Z",
  folded_under: "p1",
  is_subagent: true,
  parent_session_id: "p1",
  ...extra,
});

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  closePanel();
  reset();
  Object.assign(getState(), { machines, loaded: true });
  upsertSession(parent);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  closePanel();
  reset();
});

describe("subagentProvenance progressive disclosure", () => {
  it("omits chip when machine and harness match parent, and shows only differing parts", () => {
    expect(subagentProvenance(parent, "laptop", { agent: "claude" }, machines)).toBe("");
    expect(subagentProvenance(parent, "laptop", { agent: "codex" }, machines)).toBe("Codex");
    expect(subagentProvenance(parent, "workstation", { agent: "claude" }, machines)).toBe("Workstation");
    expect(subagentProvenance(parent, "workstation", { agent: "codex" }, machines)).toBe("Workstation · Codex");
  });
});

describe("SessionOverview Subagents section", () => {
  it("unifies embedded, same-machine (including folded_as), and cross-machine A2A subagents with Running/Ended grouping", () => {
    const sameMachineWorker = childSession({
      session_id: "local-codex",
      title: "Local Codex worker",
      agent: "codex",
      folded_as: "worker",
      state: "working",
      status: "working",
      started_at: "2026-11-24T01:01:00Z",
      activity: "Checking types",
    });
    const remoteNeedsYou = childSession({
      machine_id: "workstation",
      session_id: "remote-a2a",
      title: "Remote A2A review",
      agent: "codex",
      state: "needs_you",
      status: "blocked",
      started_at: "2026-11-24T00:50:00Z",
      folded_under: "p1",
      folded_under_machine_id: "laptop",
      parent_machine_id: "laptop",
      parent_session_id: "p1",
      a2a_task_id: "task-1",
      subagent_origin: "a2a",
    });
    const failedEnded = childSession({
      machine_id: "workstation",
      session_id: "remote-failed",
      title: "Remote failed build",
      agent: "claude",
      state: "idle",
      status: "idle",
      turn_error: "Build failed",
      folded_under: "p1",
      folded_under_machine_id: "laptop",
      started_at: "2026-11-24T00:20:00Z",
      updated_at: "2026-11-24T00:25:00Z",
    });
    const endedExtra = [1, 2, 3].map((i) =>
      childSession({
        session_id: `done-${i}`,
        title: `Done ${i}`,
        agent: i % 2 === 0 ? "codex" : "claude",
        state: "idle",
        status: "idle",
        started_at: `2026-11-24T00:1${i}:00Z`,
        updated_at: `2026-11-24T00:1${i}:30Z`,
      }),
    );

    const ix: Interaction = {
      id: "ix-remote",
      machine_id: "workstation",
      session_id: "remote-a2a",
      folded_under: "p1",
      folded_under_machine_id: "laptop",
      kind: "permission",
      tool_name: "Bash",
    };
    Object.assign(getState(), { interactions: [ix] });

    act(() =>
      root.render(
        <SessionOverview
          id="ov"
          session={parent}
          machineName="Laptop"
          items={[]}
          tasks={emptyTaskIndex()}
          children={[sameMachineWorker, remoteNeedsYou, failedEnded, ...endedExtra]}
        />,
      ),
    );

    // Running · 3 group: needs_you pinned first, then newest start first (emb-run > local-codex)
    const runningGroup = host.querySelector('[data-subagents-group="running"]')!;
    expect(runningGroup).not.toBeNull();
    const runningIds = [...runningGroup.querySelectorAll("[data-subagent-row]")].map((el) => el.getAttribute("data-subagent-row"));
    expect(runningIds).toEqual(["remote-a2a", "p1@emb-run", "local-codex"]);

    // Progressive provenance chips
    const chipOf = (sid: string) => host.querySelector(`[data-subagent-row="${sid}"] [data-subagent-provenance]`)?.textContent ?? "";
    expect(chipOf("p1@emb-run")).toBe("");
    expect(chipOf("local-codex")).toBe("Codex");
    expect(chipOf("remote-a2a")).toBe("Workstation · Codex");

    // Second-line doing summary on running items
    expect(host.querySelector('[data-subagent-row="p1@emb-run"] [data-subagent-doing]')?.textContent).toBe("Running vitest suite");
    expect(host.querySelector('[data-subagent-row="local-codex"] [data-subagent-doing]')?.textContent).toBe("Checking types");

    // Needs you status shown with attention color
    expect(host.querySelector('[data-subagent-row="remote-a2a"]')?.textContent).toContain("等你确认");

    // Ended · 5 group is collapsed by default with at most 4 stacked avatars
    const endedToggle = host.querySelector<HTMLButtonElement>("[data-subagents-ended-toggle]")!;
    expect(endedToggle.getAttribute("aria-expanded")).toBe("false");
    expect(endedToggle.textContent).toContain("5 个已结束");
    expect(host.querySelectorAll("[data-subagents-ended-avatars] > span").length).toBe(4);
    expect(host.querySelector('[data-subagent-row="remote-failed"]')).toBeNull();

    // Expand Ended group: full list appears, failed is red, provenance chip shows only differing machine ("Workstation")
    act(() => endedToggle.click());
    expect(endedToggle.getAttribute("aria-expanded")).toBe("true");
    const failedRow = host.querySelector('[data-subagent-row="remote-failed"]')!;
    expect(failedRow).not.toBeNull();
    expect(failedRow.innerHTML).toContain("text-red");
    expect(failedRow.textContent).toContain("失败");
    expect(chipOf("remote-failed")).toBe("Workstation");

    // Clicking a cross-machine row opens kind:"subagent" panel with from:"overview"
    act(() => host.querySelector<HTMLButtonElement>('[data-subagent-row="remote-a2a"]')!.click());
    expect(getPanel()).toEqual({
      kind: "subagent",
      mid: "workstation",
      sid: "remote-a2a",
      parentMid: "laptop",
      parentSid: "p1",
      call: "child:remote-a2a",
      from: "overview",
    });

    // Clicking an embedded row opens kind:"subagent" with <parent>@<agent>
    act(() => host.querySelector<HTMLButtonElement>('[data-subagent-row="p1@emb-run"]')!.click());
    expect(getPanel()).toEqual({
      kind: "subagent",
      mid: "laptop",
      sid: "p1@emb-run",
      parentMid: "laptop",
      parentSid: "p1",
      call: "call-emb-1",
      from: "overview",
    });
  });

  it("hides the Running group completely when 0 subagents are running (no empty state) and caps nesting at 2 levels", () => {
    const deepParent: Session = {
      ...parent,
      subagents: [
        { id: "d0", session_id: "p1@d0", description: "Level 0", running: false, started_at: "2026-11-24T00:01:00Z" },
        { id: "d1", session_id: "p1@d1", parent_id: "d0", description: "Level 1", running: false, started_at: "2026-11-24T00:02:00Z" },
        { id: "d2", session_id: "p1@d2", parent_id: "d1", description: "Level 2", running: false, started_at: "2026-11-24T00:03:00Z" },
        { id: "d3", session_id: "p1@d3", parent_id: "d2", description: "Level 3 flattened", running: false, started_at: "2026-11-24T00:04:00Z" },
      ],
    };
    upsertSession(deepParent);

    act(() =>
      root.render(
        <SessionOverview id="ov" session={deepParent} machineName="Laptop" items={[]} tasks={emptyTaskIndex()} children={[]} />,
      ),
    );

    expect(host.querySelector('[data-subagents-group="running"]')).toBeNull();
    const toggle = host.querySelector<HTMLButtonElement>("[data-subagents-ended-toggle]")!;
    act(() => toggle.click());

    const depths = [...host.querySelectorAll("[data-subagent-depth]")].map((li) => [
      li.querySelector("[data-subagent-row]")?.getAttribute("data-subagent-row"),
      Number(li.getAttribute("data-subagent-depth")),
    ]);
    expect(depths).toEqual([
      ["p1@d0", 0],
      ["p1@d1", 1],
      ["p1@d2", 2],
      ["p1@d3", 2],
    ]);
  });
});
