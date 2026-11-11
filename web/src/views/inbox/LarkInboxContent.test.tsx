// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { ApiError } from "../../lib/api";
import type { InboxItem } from "../../lib/inbox";
import { LarkInboxContent } from "./LarkInboxContent";
import { ConnectorReplyTarget, ConnectorReadNotice, conversationFailure } from "./ConnectorReadNotice";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let root: Root, host: HTMLDivElement;
beforeEach(() => { host=document.createElement("div");document.body.append(host);root=createRoot(host); });
afterEach(async () => { await act(async()=>root.unmount());host.remove(); });
const card = `<card title="Merge Request Merged">\n**Merge Request**\n[Example release](https://example.com/merge/1)\n**Author**\nExample author\n**Repository**\n[example/project](https://example.com/project)\n**Description**\n${"A detailed explanation of the example change. ".repeat(15)}\n</card>`;

describe("Lark Inbox message presentation", () => {
 it("keeps long content accessible while providing a preview and a collapse action", async () => {
  await act(async()=>root.render(<LarkInboxContent text={card} markdown />));
  expect(host.querySelector("article")).not.toBeNull();
  expect(host.querySelector("a")?.textContent).toBe("Example release");
  expect(host.querySelector(".line-clamp-3")).not.toBeNull();
  const expand=[...host.querySelectorAll("button")].find(b=>b.textContent===t("inbox-connectorthread.expand_description"))!;
  await act(async()=>expand.click());
  expect(host.querySelector(".line-clamp-3")).toBeNull();
  expect(host.querySelectorAll("button")).toHaveLength(2);
  expect(host.querySelector("pre")?.textContent).toBe(card);
  expect(host.querySelector("article > details")?.hasAttribute("open")).toBe(false);
 });
 it("preserves unsupported envelopes literally and does not execute source HTML", async () => {
  await act(async()=>root.render(<LarkInboxContent text={'<card title="Example" onclick="alert(1)"><script>unsafe()</script></card>'} markdown />));
  expect(host.querySelector("pre")?.textContent).toContain("<script>");
  expect(host.querySelector("script")).toBeNull();
  expect(host.querySelector("[onclick]")).toBeNull();
  await act(async()=>root.render(<LarkInboxContent text={'<card title="Example">[unsafe](javascript:alert%281%29)\n<script>unsafe()</script></card>'} markdown />));
  expect(host.querySelector('a[href^="javascript:"]')).toBeNull();
  expect(host.querySelector("script")).toBeNull();
 });
 it("shows a friendly reply destination without inventing a message preview", async () => {
  const item={source:"larkcli",title:"Example Assistant",connector:{reply_to:"om_example",can_reply:true}} as InboxItem;
  await act(async()=>root.render(<ConnectorReplyTarget item={item} />));
  const summary=host.querySelector("summary")!;
  expect(summary.textContent).toContain("Example Assistant");
  expect(summary.textContent).not.toContain("om_example");
  expect(summary.textContent).toContain(t("inbox-connectorthread.reply_unknown"));
  expect(host.querySelector("p")?.textContent).toBe("om_example");
  await act(async()=>root.render(<ConnectorReplyTarget item={item} page={{chat:{id:"example",name:"Example",kind:"dm"},messages:[{id:"om_example",at:"",sender:{name:"Example"},text:card}]}} />));
  expect(host.querySelector("summary")?.textContent).toContain("Example release");
 });
 it("only offers a retry for a transient failure", async () => {
  const retry=vi.fn();
  for(const error of [new ApiError(409,"Example has no conversations to open",{}),new ApiError(409,"Example",{code:"conversations_unsupported"}),new ApiError(429,"Example limited",{}),new ApiError(403,"Example denied",{})]) {
   await act(async()=>root.render(<ConnectorReadNotice failure={conversationFailure(error)} onRetry={retry} />));
   expect(host.querySelector("button")).toBeNull();
   expect(host.querySelector("details p")?.textContent).toBe(error.message);
  }
  await act(async()=>root.render(<ConnectorReadNotice failure={conversationFailure(new Error("Example timeout"))} onRetry={retry} />));
  await act(async()=>host.querySelector("button")!.click());expect(retry).toHaveBeenCalledOnce();
 });
});
