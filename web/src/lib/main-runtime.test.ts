import { mainConversationMachine } from "./feed-cache";
import { describe, expect, it } from "vitest";
import { mainRuntimeSummary, type MainExecution } from "./main-runtime";

const report: MainExecution = { session_id: "main", machine_id: "routing-node", source: "native", state: "running", harness: "codex", location: { host: "execution-host" } };

describe("main conversation runtime", () => {
  it("shows an execution-side location and harness, independently of the routing node", () => {
    expect(mainRuntimeSummary("main", report)).toBe("主对话 · 运行中 · execution-host · Codex");
    expect(mainRuntimeSummary("main", { ...report, location: { host: "execution-host", container: "example-container" } }))
      .toBe("主对话 · 运行中 · 容器 example-container · Codex");
  });

  it("does not confuse a proxy or missing execution fields with a confirmed location", () => {
    expect(mainRuntimeSummary("main", { ...report, source: "runtime", proxy: "Relay", location: undefined }))
      .toBe("主对话 · 运行中 · 经 Relay 接入 · Codex");
    // a missing location is left out, not said to be unreported (audit item 80)
    expect(mainRuntimeSummary("main", { ...report, location: undefined })).toBe("主对话 · 运行中 · Codex");
  });

  it("says nothing when nothing is known: no report, another conversation's, or an unknown state (audit item 80)", () => {
    expect(mainRuntimeSummary("main", { session_id: "main", machine_id: "routing-node", state: "unknown" })).toBe("");
    expect(mainRuntimeSummary("another", report)).toBe("");
    expect(mainRuntimeSummary("main", undefined)).toBe("");
  });

  it("separates no main, startup, and a last known offline location", () => {
    expect(mainRuntimeSummary(undefined)).toBe("主对话还没开始");
    expect(mainRuntimeSummary(undefined, undefined, true)).toBe("主对话 · 启动中…");
    expect(mainRuntimeSummary("main", { ...report, state: "offline" })).toBe("主对话 · 离线 · 上次运行于 execution-host · Codex");
  });

  it("preserves the main route when the configured device changes", () => {
    expect(mainConversationMachine({ main: "main", main_machine_id: "old", machine_id: "new", conversations: [] })).toBe("old");
    expect(mainConversationMachine({ main: "main", machine_id: "legacy", conversations: [] })).toBe("legacy");
  });
});
