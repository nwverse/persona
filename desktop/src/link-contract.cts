export interface LinkCommand {
  readonly action: "status" | "pair-active" | "pair-passive" | "reconnect" | "node-install" | "node-status" | "rename";
  readonly value?: string;
}

export function linkArguments(command: LinkCommand): { args: string[]; stdin?: string } | undefined {
  const value = command.value?.trim() ?? "";
  switch (command.action) {
    case "status": return { args: ["status", "--json"] };
    case "node-status": return { args: ["node", "status", "--json"] };
    case "reconnect": return { args: ["reconnect"] };
    case "node-install": return { args: ["node", "install"] };
    case "rename": return value.length > 0 && value.length <= 40 ? { args: ["name", value] } : undefined;
    case "pair-active": return value.startsWith("persona://link?") || value.startsWith("jarvis://link?") ? { args: ["pair", "-"], stdin: value } : undefined;
    case "pair-passive": return !value || /^:\d{1,5}$/.test(value) ? { args: ["pair", "--listen", ...(value ? [value] : [])] } : undefined;
  }
}

/** The CLI output is an allowlisted status view, not a dump of link.json, logs or arbitrary local paths. */
export function publicLinkStatus(value: unknown): Record<string, unknown> | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  const v = value as Record<string, unknown>;
  const out: Record<string, unknown> = {};
  for (const key of ["running", "paired", "mode", "machine", "machine_name", "hub_name", "trust", "link", "passive", "error"]) {
    if (key in v) out[key] = v[key];
  }
  return out;
}
