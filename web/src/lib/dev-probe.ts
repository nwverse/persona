// Developing Persona's probe (#9617, docs/operations/developing-persona.md): where the page's request layer, socket,
// router and error reporter tell the recorder what happened. A no-op until the recorder (lib/dev-mode.ts, its own chunk,
// loaded only while the switch is on) installs a sink, so it costs the first screen a few null checks. iOS's twin is
// apple/Shared/DevProbe.swift.
import type { ErrorReport } from "./extension-wire";

/** Where this browser keeps the switch: the moment it was turned on (ISO), absent while off. */
export const DEV_MODE_KEY = "persona.devMode";

export interface DevSink {
  request(method: string, path: string, status: number, ms: number, bytes: number): void;
  frame(type: string, bytes: number): void;
  socket(event: "connected" | "dropped"): void;
  page(name: string): void;
  report(report: ErrorReport): void;
}

let sink: DevSink | null = null;

/** The recorder's sink, or null to stop. */
export function setDevSink(next: DevSink | null) {
  sink = next;
}

/** Whether the recorder listens now (the error reporter keeps a report for it even with the hub's reports off). */
export function devListening(): boolean {
  return sink !== null;
}

/** When the switch was turned on, or null while it is off. */
export function devModeSince(): string | null {
  try {
    return localStorage.getItem(DEV_MODE_KEY);
  } catch {
    return null;
  }
}

export const devProbe = {
  request: (method: string, path: string, status: number, ms: number, bytes: number) => sink?.request(method, path, status, ms, bytes),
  frame: (type: string, bytes: number) => sink?.frame(type, bytes),
  socket: (event: "connected" | "dropped") => sink?.socket(event),
  page: (name: string) => sink?.page(name),
  report: (report: ErrorReport) => sink?.report(report),
};
