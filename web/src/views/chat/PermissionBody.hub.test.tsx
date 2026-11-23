// @vitest-environment jsdom
// The card that asks before the assistant's message goes to a partner hub (docs/server/partner-hubs.md): which hub and
// the message exactly as it is sent, each under its label, with the manager's summary (the same question and text) not
// shown a second time.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it } from "vitest";
import type { Interaction } from "../../lib/types";
import { PermissionBody } from "./PermissionCard";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

it("shows the hub and the message once each", () => {
  const text = "周六下午有空吗？想约你一起去看展。";
  const ix = {
    id: "p_hub", machine_id: "server", session_id: "main", kind: "permission", tool_name: "hub_send", session_title: "Persona",
    summary: `把这条消息发给「家里」？\n\n${text}`, tool_input: { hub: "家里", hub_id: "hub-b", message_id: `pm_${"1".repeat(32)}`, text },
  } as Interaction;
  act(() => root.render(<PermissionBody ix={ix} />));
  expect([...host.querySelectorAll("pre")].map((p) => p.textContent)).toEqual(["家里 (hub-b)", text]);
  expect(host.textContent).toContain("协作 hub");
  expect(host.textContent).toContain("消息");
  expect(host.textContent).not.toContain("把这条消息发给");
  // the raw input stays one click away
  expect(host.querySelector("button[aria-expanded]")?.getAttribute("aria-expanded")).toBe("false");
});
