import { expect, it } from "vitest";
import { testWorkers } from "./test-scheduling";

it("bounds file workers without leaving a small machine serial", () => {
  expect(testWorkers(1)).toBe(1);
  expect(testWorkers(2)).toBe(1);
  expect(testWorkers(4)).toBe(3);
  expect(testWorkers(9)).toBe(8);
  expect(testWorkers(64)).toBe(8);
  expect(testWorkers(128)).toBe(8);
});
