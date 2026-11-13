import { describe, expect, it } from "vitest";
import { interruptLook, mergeInto, type FeedItem } from "./feed";

const receipt = (id: string, body: Record<string, unknown>): FeedItem => ({
  id,
  conversation: "conv1",
  at: "2026-11-11T00:30:00Z",
  kind: "receipt",
  ref: "todo_" + id,
  body: {
    title: "Fix the login page",
    state: "interrupted",
    error: "API Error: 502",
    ...body,
  },
});

describe("interruptLook (#3834)", () => {
  it("draws a receipt from a hub before #3834 as ever", () => {
    expect(interruptLook(receipt("a", {}))).toBeUndefined();
    expect(
      interruptLook({
        ...receipt("a", { resolution: "pending" }),
        body: { state: "done", resolution: "pending" },
      }),
    ).toBeUndefined();
  });

  it("cancelled, rerouted and resumed receipts are handled lines", () => {
    for (const resolution of [
      "cancelled",
      "rerouted",
      "resumed",
      "accepted",
      "done",
    ] as const) {
      expect(interruptLook(receipt("a", { resolution }))).toEqual({
        kind: "resolved",
        resolution,
      });
    }
    expect(interruptLook(receipt("a", { resolution: "pending" }))).toEqual({
      kind: "pending",
    });
  });

  it("a batch is drawn once, on its head", () => {
    const batch = {
      cause: "model_error",
      machine: "mlx",
      total: 2,
      pending: 1,
      resolved: 1,
      tasks: [
        {
          receipt: "h",
          todo_id: "todo_h",
          title: "One",
          resolution: "cancelled",
          at: "",
        },
        {
          receipt: "m",
          todo_id: "todo_m",
          title: "Two",
          resolution: "pending",
          at: "",
        },
      ],
    };
    expect(
      interruptLook(receipt("h", { resolution: "cancelled", batch })),
    ).toEqual({ kind: "batch", batch });
    expect(
      interruptLook(receipt("m", { resolution: "pending", group: "h" })),
    ).toEqual({ kind: "hidden" });
  });

  it("a receipt re-sent with its new resolution replaces the one shown", () => {
    const before = [receipt("a", { resolution: "pending" })];
    const after = mergeInto(before, [
      receipt("a", { resolution: "cancelled" }),
    ]);
    expect(interruptLook(after[0])).toEqual({
      kind: "resolved",
      resolution: "cancelled",
    });
  });
});
