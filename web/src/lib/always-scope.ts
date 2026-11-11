// Keep the card's promise tied to the node's actual permission keys, including older cards without keys.
import { t } from "../i18n";
import { toolDisplayName } from "./turns";
import type { Interaction } from "./types";

type AlwaysCard = Pick<Interaction, "agent" | "tool_name" | "always_label" | "always_keys" | "always_honored">;

/** Whether a permission card offers "Always allow": as the manager says when it does (always_honored, #3590: its own
 *  cards keep nothing), else Claude's, and another agent's (Codex, an agent runtime) only when it says what always
 *  allows. */
export function offersAlways(ix: AlwaysCard): boolean {
  if (ix.always_honored !== undefined) return ix.always_honored;
  return !ix.agent || ix.agent === "claude" || !!ix.always_label;
}

export function alwaysScope(ix: AlwaysCard): string {
  if (!offersAlways(ix)) return "";
  if (ix.always_label) return t("chat-interactions.always_scope_named", { scope: ix.always_label });
  if (ix.agent && ix.agent !== "claude") return "";
  const keys = ix.always_keys ?? [];
  if (keys.length && keys.every((key) => /^tool:(Read|Edit)\(\/\/.*\/\*\*\)$/.test(key))) {
    return t("chat-interactions.always_scope_directory");
  }
  if (!keys.length || keys.every((key) => key === `tool:${ix.tool_name}`)) {
    return ix.tool_name === "Bash"
      ? t("chat-interactions.always_scope_commands")
      : t("chat-interactions.always_scope_tool", { tool: toolDisplayName(ix.tool_name) }); // an MCP tool in words, never mcp__…
  }
  return t("chat-interactions.always_scope_saved");
}
