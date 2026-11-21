// Descriptive picker eligibility; the hub re-attests before execution.
import { managedProvider } from "./compatibility";
import type { Launcher } from "./types";

export function launcherSupervisionPolicy(target: Launcher): boolean {
  if (!target.name || target.name.startsWith("persona_ext_")) return false;
  const native = !target.source || ["zshrc", "path", "builtin"].includes(target.source) || managedProvider(target.source);
  if (native && (!target.agent || target.agent === "claude" || target.agent === "codex")) return true;
  return Boolean(target.assistant && target.supervision_policy);
}
