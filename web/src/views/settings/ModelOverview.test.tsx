// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import fixture from "../../../../spec/fixtures/model-overview/overview.json";
import { t } from "../../i18n";
import { ApiError } from "../../lib/api";
import type { ModelOverview } from "../../lib/model-overview";
import { ModelOverviewSection, useModelOverview } from "./ModelOverview";

const mocked = vi.hoisted(() => ({
  read: vi.fn<(manager: string, signal: AbortSignal) => Promise<ModelOverview>>(),
  events: new Map<string, (message: Record<string, unknown>) => void>(),
}));
vi.mock("../../lib/model-overview", async (original) => ({
  ...(await original<typeof import("../../lib/model-overview")>()), modelOverview: mocked.read,
}));
vi.mock("../../lib/sessions", () => ({ useAgentSessions: () => [] }));
vi.mock("../../lib/store", () => ({
  onHubEvent: (type: string, listen: (message: Record<string, unknown>) => void) => {
    mocked.events.set(type, listen);
    return () => { mocked.events.delete(type); };
  },
}));
vi.hoisted(() => {
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, addEventListener() {}, removeEventListener() {} })) as typeof window.matchMedia;
});

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const snapshot = fixture as ModelOverview;
const openPurpose = vi.fn();
const ignore = () => {};
function Harness({ manager }: { manager: string }) {
  const read = useModelOverview(manager);
  return <ModelOverviewSection manager={manager} read={read} onPurpose={openPurpose} onProvider={ignore}
    onMachine={ignore} onProfile={ignore} onInventoryChanged={ignore} />;
}

let host: HTMLElement;
let root: Root;

beforeEach(() => {
  mocked.read.mockReset();
  openPurpose.mockClear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
});

describe("Models overview in Web settings", () => {
  it("shows the configured model, computer fit and a link to the existing purpose editor", async () => {
    mocked.read.mockResolvedValue(snapshot);
    await act(async () => root.render(<Harness manager="work" />));
    expect(host.querySelector("#settings-model-overview")).not.toBeNull();
    expect(host.textContent).toContain(snapshot.machines[0].name);
    expect(host.textContent).toContain(snapshot.agents.model);
    const row = [...host.querySelectorAll("button")].find((button) => button.textContent?.includes(t("settings-modeloverview.agents")));
    expect(row).toBeDefined();
    await act(async () => row?.click());
    expect(openPurpose).toHaveBeenCalledWith("agent.new");
  });

  it("hides the section on an older manager and retires its listeners", async () => {
    mocked.read.mockRejectedValue(new ApiError(404, "Not Found", {}));
    await act(async () => root.render(<Harness manager="legacy" />));
    expect(host.querySelector("#settings-model-overview")).toBeNull();
    expect(mocked.events.size).toBe(0);
  });

  it("aborts an old read and never shows its data after a manager switch", async () => {
    let oldAnswer: (data: ModelOverview) => void = () => {};
    mocked.read.mockImplementationOnce(() => new Promise((resolve) => { oldAnswer = resolve; }));
    mocked.read.mockResolvedValueOnce({ ...snapshot, machines: [], agents: { ...snapshot.agents, model: "new-manager-model" } });
    await act(async () => root.render(<Harness manager="old" />));
    const oldSignal = mocked.read.mock.calls[0][1];
    await act(async () => root.render(<Harness manager="new" />));
    expect(oldSignal.aborted).toBe(true);
    await act(async () => oldAnswer(snapshot));
    expect(host.textContent).toContain("new-manager-model");
    expect(host.textContent).not.toContain(snapshot.machines[0].name);
  });
});
