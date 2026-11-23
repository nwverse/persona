// The Workbench sidebar's grouping (#13439): the agent list by project, across computers. Pure, so it is tested without a
// page. A project is one repository on one computer (worktrees of it collapse into it, lib/agent-repos.ts repoOf); its
// label is the folder name only, never a path or an id (docs/design/DESIGN_SYSTEM.md 5.6). The rows are the complete
// list the existing sidebar reads (useFullAgentSessions), so a row's state and the counts are the same numbers.
import { stateOf } from "../../lib/format";
import { foldedState, isFoldedIn, listedKeys } from "../../lib/child-sessions";
import { repoOf } from "../../lib/agent-repos";
import type { Machine, Session } from "../../lib/types";

export interface Project {
  /** Computer and repository: unique and stable, for folding and React keys. */
  key: string;
  /** The folder's name, with the computer's name added when another project goes by the same one. */
  label: string;
  /** Where "New thread" starts: the repository, on its computer. Empty for sessions that have no folder. */
  cwd: string;
  machineId: string;
  /** Needs-you threads: the same words as the sidebar's rows (the folded state, so a child's wait counts on its parent). */
  needs: number;
  threads: Session[];
  /** Newest change among its threads, for ordering. */
  updated: number;
}

const OTHER = "\u0000other";
const time = (s: Session) => Date.parse(s.activity_at || s.updated_at || "") || 0;
const needsYou = (s: Session) => foldedState(s) === "needs_you";

function rank(s: Session): number {
  if (needsYou(s)) return 0;
  return stateOf(s) === "working" ? 1 : 2;
}

/** Threads in display order: needing you, then working, then the rest, each newest first; ties by id so it is stable. */
export function sortThreads(list: readonly Session[]): Session[] {
  return [...list].sort((a, b) => rank(a) - rank(b) || time(b) - time(a) || a.session_id.localeCompare(b.session_id));
}

/**
 * Projects for `sessions` (assistant conversations and rows hidden by a directory are left out: the Persona row stands
 * for the former, the latter are not the user's here). Projects with a thread that needs you come first, then the most
 * recently changed; sessions with no folder go last, in "Other". `other` is that group's label.
 */
export function groupByProject(sessions: readonly Session[], machines: readonly Machine[], other: string): Project[] {
  const names = new Map(machines.map((m) => [m.id, m.name]));
  const by = new Map<string, Project>();
  // a child that folds under a parent the list holds is never a row or a count of its own (the usual sidebar's rule)
  const listed = listedKeys(sessions);
  for (const s of sessions) {
    if (s.assistant || s.hidden_by_directory || isFoldedIn(s, listed)) continue;
    const r = repoOf(s.cwd ?? "", s.repo_root);
    const key = r.repo ? `${s.machine_id}\n${r.repo}` : OTHER;
    let p = by.get(key);
    if (!p) {
      p = { key, label: r.repo ? folderName(r.name) : other, cwd: r.repo, machineId: r.repo ? s.machine_id : "", needs: 0, threads: [], updated: 0 };
      by.set(key, p);
    }
    p.threads.push(s);
    if (needsYou(s)) p.needs++;
    p.updated = Math.max(p.updated, time(s));
  }
  const projects = [...by.values()];
  for (const p of projects) p.threads = sortThreads(p.threads);
  // two projects with one folder name are told apart in plain words: the computer's name, else the folder holding it
  const seen = new Map<string, Project[]>();
  for (const p of projects) if (p.key !== OTHER) seen.set(p.label, [...(seen.get(p.label) ?? []), p]);
  for (const same of seen.values()) {
    if (same.length < 2) continue;
    const distinctMachines = new Set(same.map((p) => p.machineId)).size === same.length;
    for (const p of same) {
      const parent = p.cwd.split("/").filter(Boolean).slice(-2, -1)[0];
      const extra = distinctMachines ? names.get(p.machineId) : parent;
      if (extra) p.label = `${p.label} · ${extra}`;
    }
  }
  return projects.sort((a, b) => {
    if ((a.key === OTHER) !== (b.key === OTHER)) return a.key === OTHER ? 1 : -1;
    return Number(b.needs > 0) - Number(a.needs > 0) || b.updated - a.updated || a.label.localeCompare(b.label) || a.key.localeCompare(b.key);
  });
}

/** Every thread in the order the sidebar shows them: for next / previous. */
export const flatThreads = (projects: readonly Project[]): Session[] => projects.flatMap((p) => p.threads);

/** The last folder of a name, for paths in either separator (a Windows computer's repository arrives with backslashes). */
export function folderName(name: string): string {
  const parts = name.split(/[\\/]/).filter(Boolean);
  return parts.length ? parts[parts.length - 1] : name;
}
