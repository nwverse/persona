const views = ["work", "monitoring", "files", "storage", "diagnostics", "procs", "services", "toolchain", "network", "management", "config"] as const;
export type MachineSubview = typeof views[number];
export interface MachineLocation {
  subview?: MachineSubview;
  range?: string;
  mode?: "reclaim";
}

/** Shared by web history and persona://open. Only carry query keys owned by the selected view. */
export function machineLocation(parts: string[], search: string): MachineLocation | null {
  const subview = parts[2] as MachineSubview | undefined;
  if (parts.length > 3 || (subview && !views.includes(subview))) return null;
  const query = new URLSearchParams(search);
  const range = query.get("range");
  return {
    ...(subview && { subview }),
    ...((!subview || subview === "monitoring") && range && { range }),
    ...(subview === "storage" && query.get("mode") === "reclaim" && { mode: "reclaim" }),
  };
}

export function machinePath(mid: string, { subview, range, mode }: MachineLocation): string {
  return `/machines/${encodeURIComponent(mid)}${subview ? "/" + subview : ""}${range ? "?range=" + encodeURIComponent(range) : mode ? "?mode=reclaim" : ""}`;
}
