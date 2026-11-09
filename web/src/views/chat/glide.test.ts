import { afterEach, expect, it, vi } from "vitest";
import { glide } from "./glide";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

it("ends where the row is when it gets there, not where it was when it set off", () => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "performance"] });
  vi.stubGlobal("requestAnimationFrame", (f: () => void) => setTimeout(f, 16));
  vi.stubGlobal("cancelAnimationFrame", (id: number) => clearTimeout(id));
  const el = { scrollTop: 50_000 } as HTMLElement;
  let target = 2_000;
  let done = 0;
  glide(el, () => target, () => done++);
  vi.advanceTimersByTime(300);
  expect(el.scrollTop).toBeLessThan(50_000);
  expect(el.scrollTop).toBeGreaterThan(target);
  target = 1_400; // exchanges above the row drawn on the way, smaller than guessed
  vi.advanceTimersByTime(1_500);
  expect(el.scrollTop).toBe(1_400);
  expect(done).toBe(1);
});
