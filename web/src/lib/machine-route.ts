const views = ["work", "monitoring", "files", "storage", "diagnostics", "procs", "services", "toolchain", "network", "management", "config"] as const;
export type MachineSubview = typeof views[number];
export interface MachineLocation {
  subview?: MachineSubview;
  path?: string;
  range?: string;
  mode?: "reclaim";
}

/** Shared by web history and persona://open. Only carry query keys owned by the selected view. */
export function machineLocation(parts: string[], search: string): MachineLocation | null {
  const segment = parts[2];
  const subview = (segment === "fs" ? "files" : segment) as MachineSubview | undefined;
  if (parts.length > 3 || (subview && !views.includes(subview))) return null;
  const query = new URLSearchParams(search);
  const range = query.get("range");
  const path = query.get("path") || query.get("dir");
  return {
    ...(subview && { subview }),
    ...(subview === "files" && path && { path }),
    ...((!subview || subview === "monitoring") && range && { range }),
    ...(subview === "storage" && query.get("mode") === "reclaim" && { mode: "reclaim" }),
  };
}

export function machinePath(mid: string, { subview, range, mode, path }: MachineLocation): string {
  const folder = subview === "files" && path ? "?path=" + encodeURIComponent(path) : "";
  return `/machines/${encodeURIComponent(mid)}${subview ? "/" + subview : ""}${range ? "?range=" + encodeURIComponent(range) : mode ? "?mode=reclaim" : folder}`;
}
