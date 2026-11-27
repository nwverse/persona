// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";

const io = vi.hoisted(() => ({
  assistant: undefined as { main?: string; machine_id?: string } | undefined,
  sendDraft: vi.fn(),
  navigate: vi.fn(),
}));

vi.mock("../../lib/store", async (original) => {
  const real = await original<typeof import("../../lib/store")>();
  return { ...real, getState: () => ({ ...real.getState(), assistant: io.assistant }) };
});
vi.mock("../../lib/drafts", async (original) => {
  const real = await original<typeof import("../../lib/drafts")>();
  return { ...real, sendDraft: io.sendDraft };
});
vi.mock("../../lib/router", async (original) => {
  const real = await original<typeof import("../../lib/router")>();
  return { ...real, navigate: io.navigate };
});

import { composeAssistant } from "./ExtensionView";

beforeEach(() => {
  io.assistant = undefined;
  io.sendDraft.mockReset();
  io.navigate.mockReset();
  window.history.replaceState({}, "", "/");
});

describe("composeAssistant", () => {
  it("puts a quick prompt in the main conversation's draft and opens it", () => {
    io.assistant = { main: "main-1", machine_id: "machine-1" };
    composeAssistant("What happened today?");
    expect(io.sendDraft).toHaveBeenCalledWith("machine-1/main-1", { type: "replace", text: "What happened today?" });
    expect(io.navigate).toHaveBeenCalledTimes(1);
  });

  it("puts it on the new-conversation page when there is no main conversation yet", () => {
    composeAssistant("What happened today?");
    expect(io.sendDraft).toHaveBeenCalledWith("persona-new", { type: "replace", text: "What happened today?" });
    expect(io.navigate).toHaveBeenCalledWith("/persona/new");
  });

  it("does not navigate away from a page already showing the new conversation", () => {
    window.history.replaceState({}, "", "/persona/new");
    composeAssistant("What happened today?");
    expect(io.sendDraft).toHaveBeenCalledWith("persona-new", { type: "replace", text: "What happened today?" });
    expect(io.navigate).not.toHaveBeenCalled();
  });
});
