// @vitest-environment jsdom
// A permission card that declares fields (a mail account's password for the assistant's settings tool) on the web,
// drawn by its own chunk (FieldsPermissionCard.tsx):
// it shows one input per field, a secret one masked, then Deny and Fill In & Allow. Allow stays off until every
// required field holds more than spaces, sends the values with the answer and empties them; Enter never answers; there
// is no plain Allow or Always allow. Before this card, the web drew a plain permission card and its Allow went without
// the password, which the hub refused. The hub is faked (apiOp).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
const calls = vi.hoisted(() => [] as { path: string; body: Record<string, unknown> }[]);
vi.mock("../../lib/ops", () => ({
  apiOp: vi.fn(async (path: string, init: { json: Record<string, unknown> }) => {
    calls.push({ path, body: init.json });
    return {};
  }),
}));
import { InteractionCard } from "./Interactions";
import { getState } from "../../lib/store";
import type { Interaction, Session } from "../../lib/types";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};
const buttons = () => [...host.querySelectorAll("button")].map((b) => b.textContent?.trim() || b.getAttribute("aria-label"));
const button = (label: string) => [...host.querySelectorAll("button")].find((b) => b.textContent?.trim() === label) as HTMLButtonElement | undefined;
const input = (key: string) => host.querySelector(`input[data-field="${key}"]`) as HTMLInputElement;
const type = (key: string, value: string) =>
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input(key), value);
    input(key).dispatchEvent(new Event("input", { bubbles: true }));
  });

const PW = "example app password";
const card = (extra: Partial<Interaction> = {}): Interaction => ({
  id: "ix_mail", machine_id: "m", session_id: "s", session_title: "Persona", kind: "permission", tool_name: "persona_settings",
  tool_title: "Change Persona settings", tool_input: { action: "add", setting: "mail" },
  summary: "Add the mailbox me@example.com, test the connection, then save",
  fields: [{ key: "password", label: "Password / app password", placeholder: "Most providers need an app password", secret: true }],
  ...extra,
});

// the card is a chunk of its own (FieldsPermissionCard.tsx): wait for it
const render = async (ix: Interaction) => {
  await act(async () => root.render(<InteractionCard ix={ix} />));
  await flush();
};

beforeAll(async () => {
  await import("./FieldsPermissionCard"); // the chunk in once, before the first card asks for it
});

beforeEach(() => {
  calls.length = 0;
  Object.assign(getState(), { sessions: { m: [{ machine_id: "m", session_id: "s" } as Session] }, interactions: [], answered: {} });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});

describe("带输入项的权限卡片", () => {
  it("每个输入项一个输入框，机密的打码；只有拒绝和填写并允许", async () => {
    await render(card());
    expect(host.textContent).toContain("Add the mailbox me@example.com");
    expect(host.textContent).toContain("Password / app password");
    const field = input("password");
    expect(field.type).toBe("password");
    expect(field.placeholder).toBe("Most providers need an app password");
    expect(field.getAttribute("autocomplete")).toBe("one-time-code");
    expect(field.getAttribute("name")).toBeNull();
    expect(buttons()).toContain("拒绝");
    expect(buttons()).toContain("填写并允许");
    expect(button("允许")).toBeUndefined();
    expect(button("总是允许")).toBeUndefined();
  });

  it("填好之前不能允许，只填空格也不行", async () => {
    await render(card());
    expect(button("填写并允许")!.disabled).toBe(true);
    type("password", "   ");
    expect(button("填写并允许")!.disabled).toBe(true);
    type("password", PW);
    expect(button("填写并允许")!.disabled).toBe(false);
  });

  it("填写并允许把填的值随答复发出，然后清空输入框", async () => {
    await render(card());
    type("password", PW);
    await act(async () => button("填写并允许")!.click());
    await flush();
    expect(calls).toEqual([{ path: "/interactions/ix_mail/resolve", body: { allow: true, values: { password: PW } } }]);
    expect(input("password")?.value ?? "").toBe("");
  });

  it("机密输入框里的换行等控制字符不会进去", async () => {
    await render(card());
    type("password", "abc\ndef\t");
    expect(input("password").value).toBe("abcdef");
  });

  it("回车不发送", async () => {
    await render(card());
    type("password", PW);
    await act(async () => { input("password").dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })); });
    await flush();
    expect(calls).toEqual([]);
  });

  it("拒绝不带填的值", async () => {
    await render(card());
    type("password", PW);
    await act(async () => button("拒绝")!.click());
    await flush();
    expect(calls).toHaveLength(1);
    expect(calls[0].body.allow).toBe(false);
    expect(calls[0].body).not.toHaveProperty("values");
  });

  it("可选项可以空着；只发填了的", async () => {
    await render(card({ fields: [{ key: "password", label: "Password", secret: true }, { key: "note", label: "Note", optional: true }] }));
    expect(input("note").type).toBe("text");
    type("password", PW);
    await act(async () => button("填写并允许")!.click());
    await flush();
    expect(calls[0].body).toEqual({ allow: true, values: { password: PW } });
  });

  it("没有输入项的权限卡片照旧", async () => {
    await render(card({ fields: undefined }));
    expect(button("允许")).toBeDefined();
    expect(host.querySelector("[data-permission-fields]")).toBeNull();
  });
});
