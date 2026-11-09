// @vitest-environment jsdom
// The assistant's password card (`secret_run`, #1137, #1581) on the web: it shows the computer, the purpose and every
// command exactly as they will run, a password field, Decline and Run. Run sends the typed password once, inside the
// answer, and empties the field; Enter never sends; the password is never in storage, the store, the URL or a log, and
// the field is emptied on Decline, when the card changes and when it goes. No Allow, no Always allow, no hand back; the
// quick paths (a row's answers, a notification's Allow) never take it, and the one way out to the hub (answerCard) sends
// only a bare decline or an allow with the password alone. The hub is faked (apiOp).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
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
import { answerCard } from "../../lib/card-answer";
import { NOTE_MESSAGE, planClick } from "../../lib/notification-answer";
import { quickAnswers } from "../../lib/quick-answers";
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
const field = () => host.querySelector("input") as HTMLInputElement;
const type = (value: string) =>
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(field(), value);
    field().dispatchEvent(new Event("input", { bubbles: true }));
  });

const PW = "correct horse 7";
const REFUSED = "请在卡片上答复：填密码后点运行，或拒绝";
const COMMANDS = [
  "sudo -S -k -p '' launchctl bootstrap system /Library/LaunchDaemons/com.example.backup.plist",
  'sudo -S -k -p \'\' sh -c "echo  two  spaces > /tmp/x"',
];
const card = (input: Record<string, unknown> = {}, extra: Partial<Interaction> = {}): Interaction => ({
  id: "ix_secret", machine_id: "m", session_id: "s", session_title: "Persona", kind: "permission", tool_name: "secret_run",
  tool_input: { machine: "macmini", machine_name: "Mac mini", purpose: "Install the launch daemon for the backup job", commands: COMMANDS, delivery: "stdin", ...input },
  summary: "Mac mini\nInstall the launch daemon for the backup job\n$ sudo -S -k -p '' true",
  fields: [{ key: "password", label: "Password", secret: true }],
  ...extra,
});

const session = (s: Partial<Session> = {}) => {
  Object.assign(getState(), { sessions: { m: [{ machine_id: "m", session_id: "s", ...s } as Session] }, interactions: [], answered: {} });
};
const render = (ix: Interaction) => act(() => root.render(<InteractionCard ix={ix} />));

/** Whether `needle` is anywhere in `value`: strings, arrays, objects, Maps and Sets, however deep. */
function holds(value: unknown, needle: string, seen = new WeakSet<object>()): boolean {
  if (typeof value === "string") return value.includes(needle);
  if (!value || typeof value !== "object") return false;
  if (seen.has(value)) return false;
  seen.add(value);
  if (value instanceof Map) return [...value.entries()].some(([k, v]) => holds(k, needle, seen) || holds(v, needle, seen));
  if (value instanceof Set) return [...value].some((v) => holds(v, needle, seen));
  return Object.entries(value).some(([k, v]) => k.includes(needle) || holds(v, needle, seen));
}

beforeEach(() => {
  calls.length = 0;
  session();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});

describe("密码卡片（secret_run）", () => {
  it("写明电脑、用途和逐字的命令，不指向别的设备", () => {
    render(card());
    expect(host.textContent).toContain("用你的密码在 Mac mini 上运行");
    expect(host.textContent).toContain("Install the launch daemon for the backup job");
    expect([...host.querySelectorAll("[data-secret-run-command]")].map((e) => e.textContent)).toEqual(COMMANDS);
    expect(host.textContent).not.toContain("iPhone");
    // the commands are text, never something to edit: the password field is the card's one input
    expect(host.querySelectorAll("input, textarea, [contenteditable]").length).toBe(1);
  });

  it("有密码框、拒绝和运行；没有允许、总是允许和交还电脑", () => {
    render(card());
    expect(buttons()).toEqual(["拒绝", "运行"]);
    const input = field();
    expect(input.type).toBe("password");
    expect(input.placeholder).toBe("Password");
    expect(input.getAttribute("autocomplete")).toBe("one-time-code");
    expect(input.getAttribute("spellcheck")).toBe("false");
    expect(input.getAttribute("autocapitalize")).toBe("off");
    expect(input.getAttribute("autocorrect")).toBe("off");
    // no form to submit, no name a password manager files it under
    expect(input.closest("form")).toBeNull();
    expect(input.hasAttribute("name")).toBe(false);
  });

  it("没填密码时运行点不了，填了才能点，删空又点不了", async () => {
    render(card());
    expect(button("运行")!.disabled).toBe(true);
    await act(async () => button("运行")!.click());
    expect(calls).toEqual([]);
    type(PW);
    expect(button("运行")!.disabled).toBe(false);
    type("");
    expect(button("运行")!.disabled).toBe(true);
    // the hub refuses a password of spaces alone
    type("   ");
    expect(button("运行")!.disabled).toBe(true);
  });

  it("tool_input 没有命令时用 summary 代替；连 summary 也没有时运行点不了", () => {
    render(card({ commands: undefined }));
    expect(host.querySelector("[data-secret-run-summary]")!.textContent).toBe("Mac mini\nInstall the launch daemon for the backup job\n$ sudo -S -k -p '' true");
    type(PW);
    expect(button("运行")!.disabled).toBe(false);
    render(card({ commands: undefined }, { id: "ix_secret_2", summary: undefined }));
    type(PW);
    expect(button("运行")!.disabled).toBe(true);
  });

  it("运行只发一次 allow:true 和填的密码，发出时密码框就清空", async () => {
    render(card());
    type(PW);
    await act(async () => button("运行")!.click());
    expect(field().value).toBe("");
    // a second click finds the field empty: nothing more goes out
    await act(async () => button("运行")!.click());
    await flush();
    expect(calls).toEqual([{ path: "/interactions/ix_secret/resolve", body: { allow: true, values: { password: PW } } }]);
    expect(field().value).toBe("");
  });

  it("在密码框里按 Enter 不会发出，只把焦点移开", async () => {
    render(card());
    type(PW);
    field().focus();
    expect(document.activeElement).toBe(field());
    await act(async () => {
      field().dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", code: "Enter", bubbles: true, cancelable: true }));
    });
    await flush();
    expect(calls).toEqual([]);
    expect(document.activeElement).not.toBe(field());
    expect(field().value).toBe(PW);
  });

  it("拒绝只发 allow:false，已填的密码清空", async () => {
    render(card());
    type(PW);
    await act(async () => button("拒绝")!.click());
    expect(field().value).toBe("");
    await flush();
    expect(calls).toEqual([{ path: "/interactions/ix_secret/resolve", body: { allow: false } }]);
  });

  it("换成另一张卡时，上一张填的密码不留下", () => {
    render(card());
    type(PW);
    render(card({}, { id: "ix_secret_2" }));
    expect(field().value).toBe("");
    expect(button("运行")!.disabled).toBe(true);
  });

  it("卡片离开页面时，密码框里的值也清空", () => {
    render(card());
    type(PW);
    const input = field();
    act(() => root.render(<></>));
    expect(input.isConnected).toBe(false);
    expect(input.value).toBe("");
  });

  it("整个流程不写 localStorage / sessionStorage，密码不进 store、地址和日志", async () => {
    const writes = vi.spyOn(Storage.prototype, "setItem");
    const logs = (["log", "info", "warn", "error", "debug"] as const).map((m) => vi.spyOn(console, m));
    render(card());
    type(PW);
    await act(async () => button("运行")!.click());
    await flush();
    expect(calls).toHaveLength(1);
    expect(writes).not.toHaveBeenCalled();
    expect(holds(getState(), PW)).toBe(false);
    // the card just answered keeps which field was filled in, not what was typed (as the hub's history keeps it)
    expect(getState().answered.ix_secret?.resolution).toEqual({ allow: true, values: { password: "<redacted>" } });
    expect(location.href).not.toContain(PW);
    expect(logs.some((l) => holds(l.mock.calls, PW))).toBe(false);
    expect(host.innerHTML).not.toContain(PW);
  });

  it("没有电脑名时用机器 id", () => {
    render(card({ machine_name: undefined }));
    expect(host.textContent).toContain("用你的密码在 macmini 上运行");
  });

  it("答复出口只放行两种：不带别的内容的拒绝，和只带密码的允许", async () => {
    const refused: Record<string, unknown>[] = [
      { allow: true },
      { allow: true, values: {} },
      { allow: true, values: { password: "" } },
      { allow: true, values: { password: "  " } },
      { allow: true, values: { password: 7 } },
      { allow: true, values: ["x"] },
      { allow: true, values: null },
      { allow: true, values: { password: "x", other: "y" } },
      { allow: true, values: { pass: "x" } },
      { allow: true, values: { password: "x" }, always: true },
      { allow: true, values: { password: "x" }, message: "x" },
      { allow: false, message: "x" },
      { allow: false, values: { password: "x" } },
      { dismiss: true },
      {},
    ];
    for (const body of refused) expect(await answerCard(card(), body), JSON.stringify(body)).toEqual({ ok: false, error: REFUSED });
    expect(calls).toEqual([]);
    expect(await answerCard(card(), { allow: false })).toEqual({ ok: true, already: false });
    expect(await answerCard(card(), { allow: true, values: { password: "x" } })).toEqual({ ok: true, already: false });
    expect(calls).toEqual([
      { path: "/interactions/ix_secret/resolve", body: { allow: false } },
      { path: "/interactions/ix_secret/resolve", body: { allow: true, values: { password: "x" } } },
    ]);
  });

  it("不能审批的会话：密码框、拒绝和运行都用不了", () => {
    session({ source_kind: "runtime", capabilities: { approve: false } } as Partial<Session>);
    render(card());
    expect(host.querySelector("fieldset")!.disabled).toBe(true);
    expect(button("拒绝")!.matches(":disabled")).toBe(true);
    expect(button("运行")!.matches(":disabled")).toBe(true);
    expect(field().matches(":disabled")).toBe(true);
  });

  it("快捷路径不接这张卡：列表行没有一键答复，通知上的允许不发出（没带 fields 也一样）", () => {
    for (const ix of [card(), card({}, { fields: [] })]) {
      expect(quickAnswers(ix)).toBeNull();
      const click = (action: "allow" | "deny") => planClick({ type: NOTE_MESSAGE, ix: "ix_secret", action, until: Date.now() + 60_000 }, [ix], Date.now());
      expect(click("allow")).toBeUndefined();
      expect(click("deny")).toBeUndefined();
    }
  });
});
