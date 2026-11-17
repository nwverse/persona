// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { beforeEach, afterEach, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import { setToken } from "../../lib/api";
import { MANAGER_HEADER } from "../../lib/manager-choice";
import { OutboundMessages } from "./OutboundMessages";

let host: HTMLDivElement, root: Root;
let grants: { id: string; messages: object }[], identity: string, requests: { body: Record<string, unknown>; headers: HeadersInit | undefined }[];
let approve: boolean, release: (() => void) | undefined;
const changed = vi.fn();
beforeEach(async () => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  await initI18n("en");
  setToken("synthetic-token");
  identity = "bot"; grants = []; requests = []; approve = true; release = undefined; changed.mockReset();
  vi.stubGlobal("fetch", vi.fn(async (url: string, init: RequestInit = {}) => {
    let body: object;
    if (init.method === "POST") {
      const request = JSON.parse(String(init.body));
      requests.push({ body: request, headers: init.headers });
      await new Promise<void>(resolve => { release = resolve; });
      if (approve) {
        if (request.scope) grants.push({ id: "sa_example", messages: request.scope });
        else identity = request.identity;
      }
      body = { result: approve ? "approved" : "declined" };
    } else body = url.includes("standing-authorizations") ? { revision: grants.length, authorizations: grants } : { identity };
    return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
  }));
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  await act(async () => root.render(<OutboundMessages manager="example-manager" changed={changed} />));
});
afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.unstubAllGlobals(); await initI18n("zh-Hans"); });
const button = (text: string) => [...host.querySelectorAll("button")].find(b => b.textContent?.trim() === text)!;
const target = () => host.querySelector<HTMLInputElement>("input:not([type=checkbox])")!;
const input = async (value: string) => act(async () => {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(target(), value);
  target().dispatchEvent(new Event("input", { bubbles: true }));
});

it("keeps automatic sending off and requires an exact chat or explicit all-chats choice", async () => {
  expect(requests).toHaveLength(0);
  expect(button("Request one-time approval").disabled).toBe(true);
  await input("oc_example_group");
  expect(button("Request one-time approval").disabled).toBe(false);
  await act(async () => button("Request one-time approval").click());
  expect(requests[0].body).toEqual({ revision: 0, scope: { channel: "larkcli", target: "oc_example_group", scenario: "inbox_reply", identity: "bot" } });
  expect(new Headers(requests[0].headers).get(MANAGER_HEADER)).toBe("example-manager");
  expect(host.textContent).toContain("Waiting for your permission card");
  expect(changed).not.toHaveBeenCalled();
  await act(async () => release!());
  expect(changed).toHaveBeenCalledOnce();
  expect(host.textContent).toContain("Approved setting read back");
});

it("does not claim an activation when the owner declines the permission card", async () => {
  approve = false;
  await act(async () => host.querySelector<HTMLInputElement>("input[type=checkbox]")!.click());
  await act(async () => button("Request one-time approval").click());
  expect((requests[0].body.scope as { target: string }).target).toBe("");
  await act(async () => release!());
  expect(changed).not.toHaveBeenCalled();
  expect(host.textContent).toContain("Not approved. Nothing changed.");
});

it("requests an identity change and verifies the effective value instead of optimistically applying it", async () => {
  await act(async () => {
    const select = host.querySelector("select")!;
    select.value = "user"; select.dispatchEvent(new Event("change", { bubbles: true }));
  });
  await act(async () => button("Request sender change").click());
  expect(requests[0].body).toEqual({ identity: "user" });
  expect(identity).toBe("bot");
  await act(async () => release!());
  expect(identity).toBe("user");
  expect(changed).toHaveBeenCalledOnce();
});
