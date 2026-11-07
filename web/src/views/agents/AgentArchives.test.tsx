// @vitest-environment jsdom
// "Archived agents" (views/agents/AgentArchives.tsx): lists what the hub archived, searches it, restores a row (it leaves
// the list), says when the list cannot be read, and the row menus offer Archive / Archive and close in place of
// Delete chat once the hub has archives.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Session } from "../../lib/types";

const hub = vi.hoisted(() => ({
  archived: [] as Session[],
  fail: null as Error | null,
  posted: [] as string[],
  features: ["agent_session_archives", "agent_session_archive_close"] as string[],
  listeners: new Set<() => void>(),
}));
vi.mock("../../lib/api", async (original) => {
  const real = await original<typeof import("../../lib/api")>();
  return {
    ...real,
    api: vi.fn(async (path: string, init?: { method?: string }) => {
      if (path === "/sessions/archived") {
        if (hub.fail) throw hub.fail;
        return { sessions: hub.archived };
      }
      if (init?.method === "POST") {
        hub.posted.push(path);
        hub.archived = hub.archived.filter((s) => !path.includes(`/${s.session_id}/unarchive`));
        return { ok: true };
      }
      throw new real.ApiError(404, "not found", {});
    }),
  };
});
vi.mock("../../lib/store", async (original) => {
  const real = await original<typeof import("../../lib/store")>();
  const { useSyncExternalStore } = await import("react");
  // one state object per features list: a selector reading machines gets the same array every time
  const machines = [{ id: "mac", name: "Studio", online: true }];
  let last: { features: string[]; state: ReturnType<typeof real.getState> } | undefined;
  const snapshot = (base: ReturnType<typeof real.getState>) => {
    if (last?.features !== hub.features) last = { features: hub.features, state: { ...base, features: hub.features, machines } as ReturnType<typeof real.getState> };
    return last.state;
  };
  return {
    ...real,
    refresh: async () => {},
    useStore: <T,>(select: (s: ReturnType<typeof real.getState>) => T): T =>
      useSyncExternalStore(
        (l) => (hub.listeners.add(l), () => void hub.listeners.delete(l)),
        () => select(snapshot(real.getState())),
      ),
  };
});
vi.mock("../../ui/atoms", async (original) => ({ ...(await original<typeof import("../../ui/atoms")>()), toast: vi.fn() }));
import { AgentArchives, filterArchived } from "./AgentArchives";
import { agentRowMenuItems } from "../AgentMenus";
import { t } from "../../i18n";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.IS_REACT_ACT_ENVIRONMENT = true;
  g.matchMedia = (q: string) => ({ matches: q.includes("min-width"), addEventListener() {}, removeEventListener() {} });
});

const row = (sid: string, p: Partial<Session> = {}): Session => ({
  machine_id: "mac", session_id: sid, title: `Agent ${sid}`, cwd: `/w/${sid}`, status: "offline", state: "offline", agent: "claude",
  archived: true, updated_at: "2026-11-01T09:00:00Z", ...p,
} as Session);

let root: Root;
let el: HTMLDivElement;
beforeEach(() => {
  hub.archived = [row("a", { title: "Fix the build", last_text: "Done, all green" }), row("b", { agent: "codex", title: "Release notes", updated_at: "2026-11-02T09:00:00Z" })];
  hub.fail = null;
  hub.posted = [];
  hub.features = ["agent_session_archives", "agent_session_archive_close"];
  el = document.createElement("div");
  document.body.append(el);
  root = createRoot(el);
});
afterEach(() => {
  act(() => root.unmount());
  el.remove();
});

const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};
const titles = () => [...el.querySelectorAll("[data-agent-archives] a")].map((a) => a.textContent ?? "");

describe("Archived agents", () => {
  it("lists the archived agents newest first and restores one", async () => {
    act(() => root.render(<AgentArchives onToggleSidebar={() => {}} />));
    await flush();
    expect(titles().map((x) => x.slice(0, 13))).toEqual(["Release notes", "Fix the build"]);
    const restore = [...el.querySelectorAll("[data-agent-archives] button")].find((b) => b.getAttribute("title") === t("agents-archives.restore_label", { title: "Fix the build" })) as HTMLButtonElement;
    await act(async () => restore.click());
    await flush();
    expect(hub.posted).toEqual(["/sessions/mac/a/unarchive"]);
    expect(titles()).toHaveLength(1);
    expect(titles()[0]).toContain("Release notes");
  });

  it("searches title, folder, computer, agent and last words", () => {
    const machines = [{ id: "mac", name: "Studio", online: true }] as never;
    expect(filterArchived(hub.archived, "green", machines).map((s) => s.session_id)).toEqual(["a"]);
    expect(filterArchived(hub.archived, "/w/b", machines).map((s) => s.session_id)).toEqual(["b"]);
    expect(filterArchived(hub.archived, "studio", machines)).toHaveLength(2);
    expect(filterArchived(hub.archived, "codex", machines).map((s) => s.session_id)).toEqual(["b"]);
  });

  it("says when there are none, and when the list cannot be read", async () => {
    hub.archived = [];
    act(() => root.render(<AgentArchives onToggleSidebar={() => {}} />));
    await flush();
    expect(el.textContent).toContain(t("agents-archives.empty_title"));
    act(() => root.unmount());
    root = createRoot(el);
    hub.fail = new Error("manager offline");
    act(() => root.render(<AgentArchives onToggleSidebar={() => {}} />));
    await flush();
    expect(el.textContent).toContain(t("agents-archives.load_failed"));
    expect(el.textContent).toContain("manager offline");
  });

  it("explains an older hub instead of an empty list", async () => {
    hub.features = ["terminal_stream"];
    act(() => root.render(<AgentArchives onToggleSidebar={() => {}} />));
    await flush();
    expect(el.textContent).toContain(t("agents-archives.unsupported_title"));
  });
});

describe("the row menu", () => {
  const live: Session = { machine_id: "mac", session_id: "s", title: "Live", cwd: "/w", status: "idle", state: "idle", agent: "claude", pane_id: "p" };
  const keys = (s: Session, features: string[]) =>
    agentRowMenuItems(s, { pinned: false, muted: false, onRename() {}, onDelete() {}, onArchiveClose() {}, features }).filter((i) => ["archive", "archive-close", "delete"].includes(i.key));

  it("offers Archive and Archive and close instead of Delete chat", () => {
    expect(keys(live, hub.features).map((i) => [i.key, Boolean(i.disabled)])).toEqual([["archive", false], ["archive-close", false]]);
    expect(keys({ ...live, status: "offline", state: "offline" }, hub.features).map((i) => i.key)).toEqual(["archive"]);
    expect(keys({ ...live, status: "offline", state: "offline" }, ["terminal_stream"]).map((i) => i.key)).toEqual(["delete"]);
  });

  it("shows them disabled, saying why, while the agent works", () => {
    const busy = keys({ ...live, status: "working", state: "working" }, hub.features);
    expect(busy.map((i) => [i.key, i.disabled])).toEqual([["archive", true], ["archive-close", true]]);
    expect(busy[0].detail).toBe(t("agentmenus.archive_busy"));
    expect(busy[1].detail).toBe(t("agentmenus.archive_close_busy"));
    const serving = keys({ ...live, ports: [{ port: 3000 }] }, hub.features);
    expect(serving.map((i) => [i.key, Boolean(i.disabled)])).toEqual([["archive", false], ["archive-close", true]]);
  });
});
