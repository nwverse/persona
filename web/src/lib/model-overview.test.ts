import { describe, expect, it, vi } from "vitest";
import fixture from "../../../spec/fixtures/model-overview/running-cases.json";
import { api } from "./api";
import { MANAGER_HEADER } from "./manager-choice";
import { groupOverviewSessions, modelOverview, type OverviewSession } from "./model-overview";

vi.mock("./api", () => ({ api: vi.fn() }));

describe("model overview reads", () => {
  it("scopes the snapshot request to the selected manager and forwards cancellation", () => {
    const { signal } = new AbortController();
    void modelOverview("office", signal);
    expect(api).toHaveBeenCalledWith("/model-config/overview", { headers: { [MANAGER_HEADER]: "office" }, signal });
  });
});

describe("sessions running on each computer", () => {
  it.each(fixture)("$name", ({ session, group }) => {
    const result = groupOverviewSessions([session]);
    if (group === null) expect(result).toEqual([]);
    else expect(result).toEqual([{ ...group, sessions: [session], working: 0 }]);
  });

  it("counts working sessions and separates computers, agents, models and unidentified launchers", () => {
    const first = {
      machine_id: "desk", session_id: "1", agent: "claude", model: "sonnet", launcher: "claude", state: "working",
    } as const;
    const idle = { ...first, session_id: "2", state: "idle" } as const;
    const unidentified = { ...idle, session_id: "3", launcher: "" };
    const otherModel = { ...idle, session_id: "4", model: "opus" };
    const otherAgent = { ...idle, session_id: "5", agent: "codex", launcher: "", started_by_persona: true };
    const otherComputer = { ...idle, machine_id: "laptop", session_id: "6" };
    const grouped = groupOverviewSessions([first, idle, unidentified, otherModel, otherAgent, otherComputer]);
    expect(grouped).toHaveLength(5);
    expect(grouped[0]).toEqual({
      machine_id: "desk", agent: "claude", launcher: "claude", model: "sonnet", sessions: [first, idle], working: 1,
    });
    expect(grouped[1]).toMatchObject({ launcher: null, sessions: [unidentified], working: 0 });
    expect(grouped[2]).toMatchObject({ model: "opus", sessions: [otherModel] });
    expect(grouped[3]).toMatchObject({ agent: "codex", launcher: null, sessions: [otherAgent] });
    expect(grouped[4]).toMatchObject({ machine_id: "laptop", sessions: [otherComputer] });
  });

  it("omits transcripts and failed or pending starts while retaining idle processes", () => {
    const idle = { machine_id: "desk", agent: "claude", launcher: "claude", status: "idle" };
    const notRunning: Partial<OverviewSession>[] = [
      { status: "offline" }, { state: "offline" }, { status: "interrupted" }, { starting: true },
      { start_error: "Unable to start" }, { deleted: true }, { hidden_by_directory: true },
    ];
    const result = groupOverviewSessions([...notRunning.map(s => ({ ...idle, ...s })), idle]);
    expect(result).toEqual([{
      machine_id: "desk", agent: "claude", launcher: "claude", model: "", sessions: [idle], working: 0,
    }]);
  });
});
