// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ApiError, setPairedToken, setToken } from "./api";
import { chooseManager, MANAGER_HEADER, resetManagerChoice, settleRosterDefault } from "./manager-choice";
import { changeStarCollections, useStarOrganization, type StarOrganization } from "./starred-collections";

const io = vi.hoisted(() => ({ request: vi.fn(), events: new Map<string, () => void>() }));
vi.mock("./api", async (original) => ({ ...await original<typeof import("./api")>(), api: io.request }));
vi.mock("./store", () => ({
  onHubEvent: (type: string, listener: () => void) => {
    io.events.set(type, listener);
    return () => { io.events.delete(type); };
  },
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const doc = (revision: number, assignments = {}): StarOrganization => ({
  revision, can_write: true, collections: [{ id: "review", name: "Review" }], assignments,
});
const path = "/assistant/starred/collections";
let root: Root;
let view: ReturnType<typeof useStarOrganization>;
async function show() {
  function Harness() { view = useStarOrganization(); return null; }
  await act(async () => root.render(<Harness />));
}
beforeEach(() => {
  resetManagerChoice();
  setToken("mock");
  settleRosterDefault();
  io.request.mockReset();
  root = createRoot(document.createElement("div"));
});
afterEach(async () => {
  await act(async () => root.unmount());
});

it("loads collection metadata only on the page and keeps the legacy snapshot flow available on 404", async () => {
  io.request.mockRejectedValue(new ApiError(404, "Not found", {}));
  expect(io.request).not.toHaveBeenCalled();
  await show();
  expect(io.request.mock.calls.map(([target]) => target)).toEqual([path]);
  expect(view.status).toBe("unsupported");
  expect(view.data).toBeUndefined();
  expect(view.error).toBe("");
  await act(async () => root.render(null));
  expect(io.events.size).toBe(0);
});

it("posts the revisioned bulk move and rejects an outdated edit until review", async () => {
  io.request.mockResolvedValueOnce(doc(1));
  await show();
  const change = { type: "assign", ids: ["star-a", "star-b"], collection: "review" } as const;
  io.request.mockRejectedValueOnce(new ApiError(409, "Conflict", { code: "conflict" }));
  io.request.mockResolvedValueOnce(doc(2));
  await act(async () => {
    await expect(changeStarCollections(change, 1, view.scope)).resolves.toMatchObject({ status: "conflict" });
  });
  expect(io.request.mock.calls.map(([target]) => target)).toEqual([path, path + "/assignments", path]);
  expect(io.request.mock.calls[1][1]).toMatchObject({ method: "PATCH", json: {
    revision: 1, star_ids: ["star-a", "star-b"], collection_id: "review",
  } });
  expect(view.data?.revision).toBe(2);
  expect(await changeStarCollections(change, 1, view.scope)).toMatchObject({ status: "conflict" });
  expect(io.request).toHaveBeenCalledTimes(3);

  io.request.mockResolvedValueOnce(doc(3, { "star-a": "review", "star-b": "review" }));
  await act(async () => {
    expect(await changeStarCollections(change, 2, view.scope)).toEqual({ status: "saved" });
  });
  expect(view.data?.assignments).toEqual({ "star-a": "review", "star-b": "review" });
});

it("discards a collection write across a manager or credential change", async () => {
  io.request.mockResolvedValueOnce(doc(1));
  await show();
  const oldScope = view.scope;
  let complete: (result: StarOrganization) => void = () => {};
  io.request.mockImplementationOnce(() => new Promise<StarOrganization>((resolve) => { complete = resolve; }));
  let oldWrite: ReturnType<typeof changeStarCollections>;
  await act(async () => { oldWrite = changeStarCollections({ type: "delete", id: "review" }, 1, oldScope); });
  expect(view.writing).toBe(true);
  io.request.mockResolvedValueOnce(doc(7));
  await act(async () => chooseManager("another"));
  expect(io.request.mock.lastCall?.[1].headers[MANAGER_HEADER]).toBe("another");
  expect(view.data?.revision).toBe(7);
  const calls = io.request.mock.calls.length;
  expect(await changeStarCollections({ type: "create", name: "Old" }, 1, oldScope)).toEqual({ status: "retired" });
  expect(io.request).toHaveBeenCalledTimes(calls);
  io.request.mockResolvedValueOnce(doc(8));
  await act(async () => setPairedToken("mock"));
  await act(async () => { complete(doc(2)); expect(await oldWrite!).toEqual({ status: "retired" }); });
  expect(view.data?.revision).toBe(8);
  expect(view.writing).toBe(false);
});
