// @vitest-environment jsdom
// The editor is self-contained: saving is a mocked callback, with no hub or session store involved.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n, t } from "../../i18n";
import type { Session } from "../../lib/types";
import { AgentPresentationDialog, type AgentPresentationInput } from "./AgentPresentationDialog";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const baseSession = {
  machine_id: "machine",
  session_id: "session",
  title: "Default agent title",
  cwd: "/work",
  status: "idle",
} as Session;

const onSave = vi.fn<(input: AgentPresentationInput) => Promise<void>>();
const onClose = vi.fn<() => void>();
let host: HTMLDivElement;
let root: Root;

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

async function render(session: Session = baseSession) {
  await act(async () => root.render(<AgentPresentationDialog session={session} onClose={onClose} onSave={onSave} />));
}

const dialog = () => document.querySelector<HTMLElement>("[role=dialog]");
const form = () => dialog()!.querySelector<HTMLFormElement>("form")!;
const nameInput = () => dialog()!.querySelector<HTMLInputElement>("input")!;
const tagInput = () => dialog()!.querySelector<HTMLTextAreaElement>("textarea")!;
const tags = () => [...dialog()!.querySelectorAll<HTMLElement>("[data-agent-tag]")].map((el) => el.dataset.agentTag);
const button = (label: string) => [...dialog()!.querySelectorAll<HTMLButtonElement>("button")].find((el) => el.textContent === label || el.getAttribute("aria-label") === label)!;

async function setName(value: string) {
  await act(() => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(nameInput(), value);
    nameInput().dispatchEvent(new Event("input", { bubbles: true }));
  });
}

async function setTagDraft(value: string) {
  await act(() => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(tagInput(), value);
    tagInput().dispatchEvent(new Event("input", { bubbles: true }));
  });
}

const addTags = () => act(() => button(t("agent-presentation.add_tags")).click());
const submit = () => act(async () => form().requestSubmit());

beforeEach(async () => {
  await initI18n("en");
  onSave.mockReset();
  onSave.mockResolvedValue(undefined);
  onClose.mockClear();
  vi.stubGlobal("matchMedia", (query: string) => ({ matches: false, media: query, addEventListener() {}, removeEventListener() {} }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

describe("AgentPresentationDialog", () => {
  it("saves a blank name explicitly to restore the default title", async () => {
    const custom = { ...baseSession, display_name: "Custom name", title: "Custom name", tags: ["existing"] } as Session;
    await render(custom);
    expect(dialog()!.textContent).toContain("Custom name");
    expect(dialog()!.textContent).toContain("may be the custom name");

    await setName("   ");
    await submit();

    expect(onSave).toHaveBeenCalledWith({ display_name: "", tags: ["existing"] });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("parses comma/newline batches, dedupes without changing first spelling, and removes tags", async () => {
    await render({ ...baseSession, tags: ["Existing"] } as Session);
    await setTagDraft(" Platform, research\nPLATFORM,  深层  ,Research , platform ");
    await addTags();

    expect(tags()).toEqual(["Existing", "Platform", "research", "深层"]);
    expect(tagInput().value).toBe("");

    await act(() => button(t("agent-presentation.remove_tag", { tag: "Platform" })).click());
    expect(tags()).toEqual(["Existing", "research", "深层"]);
    await submit();
    expect(onSave).toHaveBeenCalledWith({ display_name: "", tags: ["Existing", "research", "深层"] });
  });

  it("submits only once while the first save is pending and closes after it succeeds", async () => {
    const pending = deferred<void>();
    onSave.mockReturnValue(pending.promise);
    await render();

    await act(() => {
      form().requestSubmit();
      form().requestSubmit();
    });
    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onClose).not.toHaveBeenCalled();

    await act(async () => {
      pending.resolve();
      await pending.promise;
    });
    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("keeps the dialog and both drafts intact after a localized save failure", async () => {
    onSave.mockRejectedValue(new Error("offline"));
    await render({ ...baseSession, tags: ["Existing"] } as Session);
    await setName("Draft name");
    await setTagDraft("New tag");
    await addTags();
    await submit();

    expect(dialog()).not.toBeNull();
    expect(nameInput().value).toBe("Draft name");
    expect(tags()).toEqual(["Existing", "New tag"]);
    expect(dialog()!.querySelector("[role=alert]")!.textContent).toBe(t("agent-presentation.save_failed", { error: "offline" }));
    expect(onClose).not.toHaveBeenCalled();
  });
});
