import { describe, expect, it } from "vitest";
import { alwaysScope, offersAlways } from "./always-scope";
import { cardToolName } from "./turns";
import { t } from "../i18n";

describe("always permission scope", () => {
  it("says Bash allows every command, including old cards", () => {
    expect(alwaysScope({ tool_name: "Bash", always_keys: ["tool:Bash"] })).toBe(t("chat-interactions.always_scope_commands"));
    expect(alwaysScope({ tool_name: "Bash" })).toBe(t("chat-interactions.always_scope_commands"));
  });
  it("promises a directory only for the directory rules the node supplied", () => {
    expect(alwaysScope({ tool_name: "Write", always_keys: ["tool:Edit(//home/me/notes/**)"] })).toBe(t("chat-interactions.always_scope_directory"));
    expect(alwaysScope({ tool_name: "Read", always_keys: ["tool:Read"] })).toBe(t("chat-interactions.always_scope_tool", { tool: "Read" }));
    expect(alwaysScope({ tool_name: "Read" })).toBe(t("chat-interactions.always_scope_tool", { tool: "Read" }));
  });
  it("names an MCP tool in words, never by its raw name (#4966)", () => {
    const scope = alwaysScope({ tool_name: "mcp__persona__report_to_supervisor", always_keys: ["tool:mcp__persona__report_to_supervisor"] });
    expect(scope).toBe(t("chat-interactions.always_scope_tool", { tool: "Persona · report to supervisor" }));
    expect(scope).not.toContain("mcp__");
  });
  it("preserves runtime scope and does not promise unsupported always grants", () => {
    expect(alwaysScope({ agent: "codex", tool_name: "Bash", always_label: "npm test" })).toBe(t("chat-interactions.always_scope_named", { scope: "npm test" }));
    expect(alwaysScope({ agent: "codex", tool_name: "Bash" })).toBe("");
    expect(alwaysScope({ tool_name: "Bash", always_keys: ["lark:POST /messages"] })).toBe(t("chat-interactions.always_scope_saved"));
  });
  // #3590: the manager's word wins where it gives one, both ways; absent (older hubs, Agents sessions) the old rule.
  it("offers Always as the manager says, else by the card's own rule", () => {
    const manager = { tool_name: "iphone_do" };
    expect(offersAlways(manager)).toBe(true);
    expect(offersAlways({ ...manager, always_honored: false })).toBe(false);
    expect(alwaysScope({ ...manager, always_honored: false })).toBe("");
    expect(offersAlways({ ...manager, always_honored: true })).toBe(true);
    expect(offersAlways({ agent: "pi", tool_name: "Bash" })).toBe(false);
    expect(offersAlways({ agent: "pi", tool_name: "Bash", always_honored: true })).toBe(true);
    expect(offersAlways({ agent: "codex", tool_name: "Bash", always_label: "npm test", always_honored: false })).toBe(false);
  });
  it("titles the manager's own tools by their name for people", () => {
    expect(cardToolName({ tool_name: "iphone_do", tool_title: "iPhone control" })).toBe("iPhone control");
    expect(cardToolName({ tool_name: "Bash" })).toBe("Bash");
  });
});
