// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { beforeEach, afterEach, expect, it, vi } from "vitest";
vi.hoisted(() => { window.matchMedia ??= ((query: string) => ({ matches: false, media: query, addEventListener() {}, removeEventListener() {} })) as typeof window.matchMedia; });
const calls = vi.hoisted(() => [] as string[]);
vi.mock("../../lib/api", async (original) => ({ ...await original<typeof import("../../lib/api")>(), api: vi.fn(async (path: string) => { calls.push(path); return { ok: true, close: { closed: true, outcome: "closed" } }; }) }));
import { InteractionCard } from "./Interactions";
import { appArchiveDeps } from "../../lib/agent-archives";
import { getState } from "../../lib/store";
import { t } from "../../i18n";
import type { Interaction, Session } from "../../lib/types";
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement;
let root: Root;
const ix: Interaction = { id: "terminal-card", machine_id: "m", session_id: "s", kind: "screen", screen: "An unrecognized permission confirmation" };
const render = () => act(() => root.render(<InteractionCard ix={ix} />));
const button = (label: string) => [...host.querySelectorAll("button")].find(b => b.textContent?.trim() === label);
const click = async (b: HTMLButtonElement | undefined) => { expect(b).toBeTruthy(); await act(async () => b!.click()); };
beforeEach(() => {
 calls.length=0;
 vi.spyOn(appArchiveDeps,"refresh").mockResolvedValue(undefined);
 vi.spyOn(appArchiveDeps,"drop").mockImplementation(() => {});
 vi.spyOn(appArchiveDeps,"notify").mockImplementation(() => {});
 Object.assign(getState(), { sessions: { m: [{ machine_id:"m",session_id:"s",agent:"claude",status:"blocked",state:"needs_you" } as Session] }, interactions:[ix], answered:{} });
 host=document.createElement("div"); document.body.append(host);root=createRoot(host);
});
afterEach(() => { act(() => root.unmount());host.remove();vi.restoreAllMocks(); });
it("offers a confirmed close on an unparsed terminal card and sends a force archive", async () => {
 render();
 await click(button(t("chat-interactions.close_agent")));
 expect(calls).toEqual([]);
 await click(button(t("chat-interactions.close_agent_confirm")));
 expect(calls).toContain("/sessions/m/s/archive?force=1");
 expect(calls.some(p => p.includes("/answer"))).toBe(false);
});
it("cancelling close sends nothing", async () => {
 render();await click(button(t("chat-interactions.close_agent")));await click(button(t("chat-interactions.close_agent_cancel")));
 expect(calls).toEqual([]);
});
it("does not offer close for a working Agent", () => {
 getState().sessions.m[0].status="working";getState().sessions.m[0].state="working";
 render();expect(button(t("chat-interactions.close_agent"))).toBeUndefined();
});

it("a read-only source cannot close from its terminal card", () => {
 Object.assign(getState().sessions.m[0], { source_kind:"runtime",capabilities:{read:true,approve:false,close:false} });
 render();const close=button(t("chat-interactions.close_agent"));
 expect(!close || close.disabled || close.closest("fieldset[disabled]")).toBeTruthy();
 expect(calls).toEqual([]);
});
