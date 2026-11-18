// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { openerIcon } from "./opener-icons";

// The installed extensions as the hub's merged list stamps them (docs/protocol/protocol.md "Per-manager routing").
const icon = (id: string, version = "1.0.0") => `/api/v1/extensions/${id}/-/icon?v=${version}`;
const list = [
  { id: "bridge", manager: "assistant", icon_url: icon("bridge") },
  { id: "plain", manager: "assistant" },
  { id: "public", manager: "assistant", icon_url: "https://images.example/public.png" },
  { id: "bridge", manager: "m-2", icon_url: icon("bridge", "2.0.0") },
  { id: "only-m2", manager: "m-2", icon_url: icon("only-m2") },
];

describe("the icon of the extension that opened a conversation (#10358)", () => {
  it("is that extension's own hub icon, asked of the manager whose extension it is", () => {
    expect(openerIcon(list, "bridge", undefined, "assistant", "assistant")).toEqual({ url: icon("bridge"), manager: "assistant" });
    expect(openerIcon(list, "bridge", "assistant", "m-2", "assistant")).toEqual({ url: icon("bridge"), manager: "assistant" });
    // another manager's conversation: that manager's copy, never the default's
    expect(openerIcon(list, "bridge", "m-2", "assistant", "assistant")).toEqual({ url: icon("bridge", "2.0.0"), manager: "m-2" });
    // a conversation without a manager is the shown assistant's
    expect(openerIcon(list, "only-m2", undefined, "m-2", "assistant")).toEqual({ url: icon("only-m2"), manager: "m-2" });
    // an item without a manager is the default's (an older hub's list); the request then names none
    expect(openerIcon([{ id: "bridge", icon_url: icon("bridge") }], "bridge", undefined, "assistant", "assistant")).toEqual({ url: icon("bridge"), manager: "" });
  });

  it("is none while the list is unknown, and for an extension without a hub icon or not installed for that manager", () => {
    expect(openerIcon(undefined, "bridge", undefined, "assistant", "assistant")).toBeUndefined();
    expect(openerIcon(list, "plain", undefined, "assistant", "assistant")).toBeUndefined();
    // public artwork stays off the rows: only the hub's own icon routes (lib/extension-logo.ts)
    expect(openerIcon(list, "public", undefined, "assistant", "assistant")).toBeUndefined();
    expect(openerIcon(list, "removed", undefined, "assistant", "assistant")).toBeUndefined();
    expect(openerIcon(list, "only-m2", "assistant", "assistant", "assistant")).toBeUndefined();
    expect(openerIcon([{ id: "bad", icon_url: "/api/v1/extensions/bad/-/icon?v=1&token=x" }], "bad", undefined, "assistant", "assistant")).toBeUndefined();
  });
});
