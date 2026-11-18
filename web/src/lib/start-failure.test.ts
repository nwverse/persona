// A failed start's reason in plain words on the hand-off card and the Needs you row (#8237, iOS StartFailureWords): the
// raw error stays in the task's details.
import { describe, expect, it } from "vitest";
import { t } from "../i18n";
import { startFailureCardLine, startFailureCause, startFailureCodeLine, startFailurePlain, startFailureRowLine } from "./start-failure";

describe("a failed start's reason", () => {
  it("names the known causes in a raw error", () => {
    expect(startFailureCause("rpc: connection closed")).toBe("connection_lost");
    expect(startFailureCause("和「MacBook」的连接断了，等了一阵也没重连上：rpc: connection closed")).toBe("connection_lost"); // i18n-ignore
    expect(startFailureCause("unexpected EOF")).toBe("connection_lost");
    expect(startFailureCause("context deadline exceeded")).toBe("timed_out");
    expect(startFailureCause("dial tcp: connect: connection refused")).toBe("offline");
    expect(startFailureCause("ssh: handshake failed: permission denied (publickey)")).toBe("not_allowed");
    expect(startFailureCause("rpc: unknown method agent.start")).toBe("needs_update");
    expect(startFailureCause("exit status 1")).toBeUndefined();
    expect(startFailureCause("the geoff error")).toBeUndefined(); // "eof" inside a word is no end of file
  });

  it("keeps a sentence the hub wrote for people, and says to open the task for any other error", () => {
    expect(startFailurePlain("这台电脑上没装 Claude Code，装好后再试")).toBe("这台电脑上没装 Claude Code，装好后再试"); // i18n-ignore
    const english = "Claude Code isn't installed on this computer. Install it, then retry.";
    expect(startFailurePlain(english)).toBe(english);
    expect(startFailurePlain("exit status 1")).toBeUndefined();
    expect(startFailurePlain("  ")).toBeUndefined();
    expect(startFailureRowLine("exit status 1")).toBe(t("assistant-tasks.start_failed_details"));
    expect(startFailureRowLine("rpc: connection closed")).toBe(t("assistant-tasks.start_cause_connection_lost"));
    expect(startFailureCardLine("rpc: connection closed")).toBe(t("assistant-tasks.start_failed", { error: t("assistant-tasks.start_cause_connection_lost") }));
    expect(startFailureCardLine("exit status 1")).toBe(t("assistant-tasks.start_failed_details"));
  });

  it("reads the plan task's code before the text: a launcher the terminal can't run, a workspace in the way (#10365)", () => {
    // the hub's long sentence (the shells, the file it could not run) stays for the details; Chinese text no longer wins
    const long = "示例电脑 上新开的终端标签页（bash）运行不了启动模型入口「work」要用的「/opt/p/personad」……"; // i18n-ignore
    const terminal = { error_code: "launcher_missing", error_args: { where: "terminal", launcher: "work", missing: "/opt/p/personad" } };
    const short = t("assistant-tasks.start_code_launcher_terminal", { launcher: "work" });
    expect(startFailureCodeLine(terminal)).toBe(short);
    expect(startFailureRowLine(long, terminal)).toBe(short);
    expect(startFailureCardLine(long, terminal)).toBe(t("assistant-tasks.start_failed", { error: short }));
    expect(startFailureRowLine("checkout path exists", { error_code: "worktree_path_taken", error_args: { path: "/w/task" } })).toBe(t("assistant-tasks.start_code_worktree_path_taken"));
    // a launcher missing from the computer's list, or a code the page does not know: the text says it
    expect(startFailureCodeLine({ error_code: "launcher_missing", error_args: { where: "list", launcher: "work" } })).toBeUndefined();
    expect(startFailureRowLine("rpc: connection closed", { error_code: "something_new" })).toBe(t("assistant-tasks.start_cause_connection_lost"));
    expect(startFailureRowLine(long)).toBe(long);
  });
});
