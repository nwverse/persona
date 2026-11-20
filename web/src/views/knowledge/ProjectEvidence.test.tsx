// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import { setToken } from "../../lib/api";
import type { KBProjectFreshness } from "../../lib/kb";
import { resetManagerChoice, setRosterDefault } from "../../lib/manager-choice";
import { lists, searches } from "./cache";
import { EntryDetail } from "./KBDetail";
import { ListPane } from "./KBList";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const initial = (): KBProjectFreshness => ({
  needs_review: true, timeline_end: 125, evidence: [{ ref: "chats/sample", position: "new-revision", source_id: "github:example/project#1" }],
  fields: {
    summary: { needs_review: true, review_reason: "new_evidence", cutoff: { timeline_from: 10, timeline_through: 40, timeline_truncated: true, sources: [{ ref: "chats/sample", position: "old-revision" }] }, uncovered_evidence_count: 3, lock_reason: "field_locked", review_by: "user" },
    one_line: { needs_review: false, cutoff: { timeline_from: 0, timeline_through: 125 }, uncovered_evidence_count: 0, review_by: "persona", writer: "persona", source: "kb.project" },
  },
});
const entry = { kind: "projects", id: "sample", name: "Sample project", one_line: "The changes merged.", relevance: 0, extra: "short · active" };
const metadata = { ...entry, kind: "short", status: "active", pinned: "A historical correction", source_urls: ["https://example.org/project"] };
let knowledge: ReturnType<typeof initial> | undefined;
let refs: string[];
let mode: string | undefined;
let writes: { body: Record<string, unknown>; manager: string | null }[];
let rejectSave: boolean;
let host: HTMLDivElement;
let root: Root;
const noop = () => {};
function Page({ list = false }: { list?: boolean }) {
  const [reloadKey, setReloadKey] = useState(0);
  return list ? <ListPane kind="projects" detailOpen={false} reloadKey={reloadKey} onOpen={noop} onToggleSidebar={noop} /> :
    <EntryDetail kind="projects" id="sample" reloadKey={reloadKey} onToggleSidebar={noop} onOpen={noop} onBack={noop} onSaved={() => setReloadKey(k => k + 1)} />;
}
const button = (name: string) => Array.from(host.querySelectorAll<HTMLButtonElement>("button")).find(b => b.textContent?.trim() === name);
const openEvidence = () => {
  const summary = Array.from(host.querySelectorAll("summary")).find(el => el.textContent?.includes("Sources and coverage"));
  expect(summary).toBeDefined();
  act(() => summary!.click());
  return summary!.closest("details")!;
};
const click = async (el?: HTMLElement | null) => { expect(el).toBeTruthy(); await act(async () => el!.click()); };
beforeEach(async () => {
  await initI18n("en");
  localStorage.clear();
  resetManagerChoice();
  setToken("synthetic-owner");
  setRosterDefault("sample-manager");
  lists.clear();
  searches.clear();
  knowledge = initial();
  mode = undefined;
  refs = [...metadata.source_urls];
  writes = [];
  rejectSave = false;
  vi.stubGlobal("fetch", vi.fn(async (url: string, init: RequestInit) => {
    let result: unknown = {};
    if (url === "/api/v1/kb/projects") result = { items: [{ ...entry, freshness: knowledge, project_mode: mode }] };
    if (url === "/api/v1/kb/projects/sample") {
      if (init.method === "PUT") {
        const body = JSON.parse(String(init.body));
        writes.push({ body, manager: new Headers(init.headers).get("X-Persona-Manager") });
        if (rejectSave) return Response.json({ error: "Pointer is invalid" }, { status: 400 });
        if (body.project_mode) mode = body.project_mode === "full" ? undefined : body.project_mode;
        if (body.source_urls) refs = body.source_urls;
      } else {
        result = { detail: { kind: "projects", id: "sample", path: "", summary: "Two items still pending.", meta: { ...metadata, source_urls: refs }, links: [{ kind: "chats", id: "sample", name: "Change request", relevance: 0 }], freshness: knowledge, project_mode: mode, sources: knowledge && [{ id: "github:example/project#1", url: "https://github.com/example/project/pull/1", refs: ["chats/sample"], delivery_state: "merged", environment_verification: "unverified" }, ...refs.map(url => ({ id: url, url }))] } };
      }
    }
    return Response.json(result);
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  resetManagerChoice();
  vi.unstubAllGlobals();
});
it("shows the review gap beside the project in the list", async () => {
  await act(async () => root.render(<Page list />));
  const row = host.querySelector('a[href*="/kb/projects/sample"]');
  expect(row?.textContent).toContain("Summary needs review");
  expect(row?.textContent).toContain("3 newer updates");
  expect(row?.textContent).not.toContain("Overview needs review");
});
it("shows the same gap on the detail and discloses coverage, locks, unknown writer and source state", async () => {
  await act(async () => root.render(<Page />));
  expect(host.textContent).toContain("Summary needs review");
  expect(host.textContent).toContain("Two items still pending.");
  expect(host.querySelector("details")?.open).toBe(false);
  const detail = openEvidence();
  expect(detail.open).toBe(true);
  for (const value of ["Timeline bytes 10–40 of 125", "Timeline bytes 0–125 of 125", "Earlier entries were outside this window", "Locked", "Writer unknown", "Written by Persona", "kb.project", "old-revision", "Source status: Merged", "Environment unverified", "github:example/project#1"]) expect(detail.textContent).toContain(value);
  expect(detail.querySelector('a[href*="/kb/chats/sample"]')?.textContent).toBe("Change request");
  expect(detail.querySelector('a[href="https://github.com/example/project/pull/1"]')).not.toBeNull();
  const revisions = Array.from(detail.querySelectorAll("details"));
  expect(revisions).toHaveLength(2);
  expect(revisions.every(section => !section.open)).toBe(true);
  await click(revisions.find(section => section.querySelector("summary")?.textContent === "Current source revisions")?.querySelector("summary"));
  expect(revisions.find(section => section.open)?.textContent).toContain("new-revision");
  expect(writes).toHaveLength(0);
});
it("writes only the changed policy, preserves existing history, and can return to managed storage", async () => {
  await act(async () => root.render(<Page />));
  openEvidence();
  await click(button("Edit storage"));
  await click(host.querySelector<HTMLButtonElement>('[role="switch"]'));
  expect(writes).toHaveLength(0);
  await click(button("Save"));
  expect(writes).toEqual([{ body: { project_mode: "reference-only" }, manager: "sample-manager" }]);
  expect(host.textContent).toContain("Two items still pending.");
  expect(host.textContent).toContain("A historical correction");
  const facts = Array.from(host.querySelectorAll("section")).find(s => s.querySelector("h2")?.textContent === "Confirmed facts");
  expect(facts?.querySelector("button")).toBeNull();
  await click(button("Edit storage"));
  await click(host.querySelector<HTMLButtonElement>('[role="switch"]'));
  await click(button("Save"));
  expect(writes[1]?.body).toEqual({ project_mode: "full" });
});
it("retains a rejected edit and changes only explicit source references; linked chats stay separate", async () => {
  await act(async () => root.render(<Page />));
  openEvidence();
  await click(button("Edit storage"));
  const input = host.querySelector("textarea")!;
  expect(input.value).toBe("https://example.org/project");
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, "https://example.org/notes");
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  rejectSave = true;
  await click(button("Save"));
  expect(input.value).toBe("https://example.org/notes");
  expect(host.querySelector('[role="alert"]')?.textContent).toContain("Pointer is invalid");
  rejectSave = false;
  await click(button("Save"));
  expect(writes[1]?.body).toEqual({ source_urls: ["https://example.org/notes"] });
  expect(host.querySelector('a[href*="/kb/chats/sample"]')).not.toBeNull();
});
it("keeps a legacy project readable without offering unsupported edits or inferring coverage", async () => {
  knowledge = undefined;
  await act(async () => root.render(<Page />));
  expect(host.textContent).toContain("Two items still pending.");
  expect(host.textContent).not.toContain("Sources and coverage");
  expect(host.textContent).not.toContain("needs review");
  expect(writes).toHaveLength(0);
});

it("keeps historical coverage and writers unknown, including a locked field", async () => {
  knowledge = { needs_review: false, timeline_end: 0, fields: { summary: { needs_review: false, lock_reason: "field_locked", review_by: "user" }, one_line: { needs_review: false, review_by: "persona" } } };
  await act(async () => root.render(<Page />));
  const evidence = openEvidence().textContent!;
  expect(evidence.match(/Coverage unknown/g)).toHaveLength(2);
  expect(evidence.match(/Writer unknown/g)).toHaveLength(2);
  expect(evidence).toContain("Locked");
  expect(evidence).toContain("Review by you");
  expect(evidence).toContain("Review by Persona");
  expect(evidence).not.toContain("Timeline bytes");
  expect(evidence).not.toContain("Written by you");
});

it("keeps overlapping field gaps separate, including a gap without a historical cutoff", async () => {
  knowledge = initial();
  knowledge.fields.one_line = { needs_review: true, uncovered_evidence_count: 1, review_reason: "coverage_unknown", review_by: "persona" };
  await act(async () => root.render(<Page list />));
  const row = host.querySelector('a[href*="/kb/projects/sample"]')!;
  expect(row.textContent).toContain("Summary needs review · 3 newer updates");
  expect(row.textContent).toContain("Overview needs review · 1 update to review");
  expect(row.textContent).not.toContain("4 newer updates");
});


it("lets the owner set policy before a new project's first evidence", async () => {
  knowledge = { needs_review: false, timeline_end: 0, fields: {} };
  await act(async () => root.render(<Page />));
  const detail = openEvidence();
  expect(detail.textContent).not.toContain("Writer unknown");
  await click(button("Edit storage"));
  await click(host.querySelector<HTMLButtonElement>('[role="switch"]'));
  await click(button("Save"));
  expect(writes[0]?.body).toEqual({ project_mode: "reference-only" });
});

it("preserves an unrecognized storage mode without offering writes that assume full mode", async () => {
  mode = "archive-preview";
  await act(async () => root.render(<Page />));
  const detail = openEvidence();
  expect(detail.textContent).toContain("archive-preview");
  expect(button("Edit storage")).toBeUndefined();
  expect(writes).toHaveLength(0);
});
