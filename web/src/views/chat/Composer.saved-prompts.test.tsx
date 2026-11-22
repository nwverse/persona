// @vitest-environment jsdom
// Saved prompts in the composer's "/" list (#7329): read once a box has the focus, listed after the chat's own commands
// with "Saved" as their source, filtered as the rest are, and picking one puts its text in the box (never sends it); a
// change on another device (assistant_saved_prompts) shows in an open box. A box with no commands of its own still offers
// them.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { t } from "../../i18n";

const hub = vi.hoisted(() => ({
  prompts: [] as { id: string; name: string; text: string; template?: true }[],
  templates: false,
  listeners: new Map<string, Set<(m: Record<string, unknown>) => void>>(),
}));
vi.mock("../../lib/api", async (original) => {
  const real = await original<typeof import("../../lib/api")>();
  return {
    ...real,
    api: vi.fn(async (path: string) => {
      if (path === "/assistant/saved-prompts") return { prompts: hub.prompts, ...(hub.templates && { template_version: 1 }) };
      throw new real.ApiError(404, "not found", {});
    }),
  };
});
vi.mock("../../lib/store", async (original) => ({
  ...(await original<typeof import("../../lib/store")>()),
  onHubEvent: (type: string, l: (m: Record<string, unknown>) => void) => {
    if (!hub.listeners.has(type)) hub.listeners.set(type, new Set());
    hub.listeners.get(type)!.add(l);
    return () => void hub.listeners.get(type)?.delete(l);
  },
}));
import { setPairedToken } from "../../lib/api";
import { chooseManager, resetManagerChoice } from "../../lib/manager-choice";
import { onPermissionPicker } from "../../lib/permissions-command";
import { resetSavedPrompts } from "../../lib/saved-prompts";
import { Composer, type Outgoing } from "./Composer";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
const box = () => host.querySelector("textarea")!;
const typeInto = (text: string, field = box()) =>
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(field, text);
    field.dispatchEvent(new Event("input", { bubbles: true }));
  });
const key = (name: string, field = box()) => act(() => void field.dispatchEvent(new KeyboardEvent("keydown", { key: name, bubbles: true, cancelable: true })));
const rows = () => [...host.querySelectorAll("[role=option]")].map((o) => o.textContent);
const dialog = () => document.querySelector<HTMLDivElement>("[role=dialog]");
const action = (name: string) => [...dialog()!.querySelectorAll("button")].find((button) => button.textContent === name)!;
const until = async (check: () => void) => {
  for (let i = 0; i < 200; i++) {
    await act(async () => { await new Promise((ok) => setTimeout(ok, 5)); });
    try {
      return check();
    } catch {
      // not yet
    }
  }
  check();
};
const saved = (name: string) => `${name}${t("chat-savedprompts.source")}`;

beforeEach(() => {
  hub.templates = false;
  hub.prompts = [
    { id: "sp1", name: "review", text: "Review this diff for security issues." },
    { id: "sp2", name: "standup", text: "Write today's standup from the commits." },
  ];
  hub.listeners.clear();
  vi.stubGlobal("matchMedia", (q: string) => ({ matches: q.includes("fine"), addEventListener() {}, removeEventListener() {} }));
  localStorage.clear();
  resetManagerChoice();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
  resetSavedPrompts();
});

it("lists them after the chat's own commands, filters them, and picking one fills the box without sending", async () => {
  const onSend = vi.fn(async (_: Outgoing) => true);
  act(() => root.render(<Composer draftKey="sp" placeholder="" onSend={onSend} />));
  act(() => box().focus());
  typeInto("/");
  await until(() => expect(rows().slice(-2)).toEqual([saved("review"), saved("standup")]));
  expect(rows().length).toBeGreaterThan(2);
  typeInto("/stand");
  expect(rows()).toEqual([saved("standup")]);
  key("Enter");
  expect(box().value).toBe("Write today's standup from the commits.");
  expect(onSend).not.toHaveBeenCalled();
});

it("shows another device's change in an open box", async () => {
  act(() => root.render(<Composer draftKey="sp" placeholder="" onSend={async () => true} />));
  act(() => box().focus());
  typeInto("/re");
  await until(() => expect(rows()).toContain(saved("review")));
  hub.prompts = [{ id: "sp1", name: "release", text: "Write the release notes." }];
  act(() => hub.listeners.get("assistant_saved_prompts")?.forEach((l) => l({ type: "assistant_saved_prompts" })));
  await until(() => expect(rows()).toContain(saved("release")));
  expect(rows()).not.toContain(saved("review"));
});

it("are offered where the chat has no commands of its own", async () => {
  act(() => root.render(<Composer draftKey="none" placeholder="" onSend={async () => true} commands={[]} />));
  typeInto("/");
  await until(() => expect(rows()).toEqual([saved("review"), saved("standup")]));
});

it("fills one field for each name and inserts literal values without sending or opening a built-in picker", async () => {
  hub.templates = true;
  hub.prompts = [
    { id: "sp3", name: "entry", text: "{{command}}", template: true },
    { id: "sp4", name: "review", text: "{{topic}} → {{topic}} ({{audience}})", template: true },
  ];
  const onSend = vi.fn(async (_: Outgoing) => true);
  const openPicker = vi.fn();
  const unregister = onPermissionPicker("templates", openPicker);
  try {
    act(() => root.render(<Composer draftKey="templates" placeholder="" onSend={onSend} />));
    typeInto("/entry");
    await until(() => expect(rows()).toEqual([saved("entry")]));
    key("Tab");
    await until(() => expect(dialog()).not.toBeNull());
    expect(box().value).toBe("/entry");
    const command = dialog()!.querySelector("textarea")!;
    expect(command.required).toBe(true);
    expect(action(t("prompt-template.insert")).disabled).toBe(true);
    typeInto(" \t\n ", command);
    expect(action(t("prompt-template.insert")).disabled).toBe(true);
    typeInto("/permissions", command);
    expect(action(t("prompt-template.insert")).disabled).toBe(false);
    act(() => action(t("prompt-template.insert")).click());
    expect(box().value).toBe("/permissions");
    await until(() => expect(document.activeElement).toBe(box()));
    expect(box().selectionStart).toBe("/permissions".length);
    typeInto("/review");
    await until(() => expect(rows()).toEqual([saved("review")]));
    act(() => host.querySelector<HTMLButtonElement>("[role=option]")!.click());
    await until(() => expect(dialog()!.querySelectorAll("textarea")).toHaveLength(2));
    expect([...dialog()!.querySelectorAll("label")].map((label) => label.textContent)).toEqual(["topic", "audience"]);
    const [topic, audience] = dialog()!.querySelectorAll("textarea");
    typeInto(" {{literal}} ", topic);
    expect(action(t("prompt-template.insert")).disabled).toBe(true);
    typeInto("team", audience);
    act(() => action(t("prompt-template.insert")).click());
    expect(box().value).toBe(" {{literal}}  →  {{literal}}  (team)");
    expect(onSend).not.toHaveBeenCalled();
    expect(openPicker).not.toHaveBeenCalled();
  } finally {
    unregister();
  }
});

it("preserves the slash draft on Cancel, provides keyboard selection, and retires a draft-key round trip", async () => {
  hub.templates = true;
  hub.prompts = [{ id: "sp3", name: "entry", text: "Write {{topic}}", template: true }];
  const render = (draftKey: string) => act(() => root.render(<Composer draftKey={draftKey} placeholder="" onSend={async () => true} />));
  const open = async () => {
    typeInto("/entry");
    await until(() => expect(rows()).toEqual([saved("entry")]));
    key("Enter");
    await until(() => expect(dialog()).not.toBeNull());
  };
  render("templates");
  await open();
  const field = dialog()!.querySelector("textarea")!;
  typeInto("private", field);
  act(() => field.dispatchEvent(new CompositionEvent("compositionstart", { bubbles: true })));
  key("Escape", field);
  expect(dialog()).not.toBeNull();
  act(() => action(t("common.cancel")).click());
  expect(box().value).toBe("/entry");
  await until(() => expect(document.activeElement).toBe(box()));
  typeInto("/entr");
  await until(() => expect(rows()).toEqual([saved("entry")]));
  key("Enter");
  await until(() => expect(dialog()).not.toBeNull());
  expect(dialog()!.querySelector("textarea")?.value).toBe("");
  render("other");
  expect(dialog()).toBeNull();
  render("templates");
  expect(dialog()).toBeNull();
});

it("retires manager and credential round trips while a template contains local values", async () => {
  hub.templates = true;
  hub.prompts = [{ id: "sp3", name: "entry", text: "Write {{topic}}", template: true }];
  act(() => root.render(<Composer draftKey="templates" placeholder="" onSend={async () => true} />));
  const open = async () => {
    typeInto("/entr");
    typeInto("/entry");
    await until(() => expect(rows()).toEqual([saved("entry")]));
    key("Enter");
    await until(() => expect(dialog()).not.toBeNull());
    typeInto("private", dialog()!.querySelector("textarea")!);
  };
  await open();
  await act(async () => { chooseManager("other"); chooseManager(null); });
  expect(dialog()).toBeNull();
  expect(box().value).toBe("/entry");
  await open();
  await act(async () => { setPairedToken("other"); setPairedToken(""); });
  expect(dialog()).toBeNull();
  expect(box().value).toBe("/entry");
});

it("preserves literal braces when the server doesn't advertise template support", async () => {
  hub.prompts = [{ id: "sp3", name: "entry", text: "Write {{topic}}", template: true }];
  act(() => root.render(<Composer draftKey="templates" placeholder="" onSend={async () => true} />));
  typeInto("/entry");
  await until(() => expect(rows()).toEqual([saved("entry")]));
  key("Enter");
  expect(dialog()).toBeNull();
  expect(box().value).toBe("Write {{topic}}");
});
