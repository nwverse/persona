// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
vi.hoisted(() => { window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {} })) as typeof window.matchMedia; });
vi.mock("../../ui/Markdown", () => ({ Markdown: () => null }));
vi.mock("../../lib/managers", () => ({
  useRoster: () => ({ default: "assistant", managers: [{ id: "assistant", name: "Personal" }] }),
  managerOrder: (r: { managers: unknown[] }) => r.managers,
}));
vi.mock("../assistant/Persona", () => ({ usePersona: () => ({ name: "Persona" }), PersonaAvatar: () => null }));
vi.mock("./Skills", () => ({ SkillsGroup: () => null }));
vi.mock("../../lib/store", () => ({ useStore: () => [], refreshAssistant: vi.fn() }));
vi.mock("../../ui/atoms", async (original) => ({ ...await original<typeof import("../../ui/atoms")>(), toast: vi.fn() }));
import { initI18n, t } from "../../i18n";
import { resetManagerChoice, setRosterDefault } from "../../lib/manager-choice";
import { toast } from "../../ui/atoms";
import { AssistantSettings } from "./AssistantSettings";

// Settings › Persona › Recordings (#3780): the two retentions, only from a hub that says them, each saved on leaving
// the field within its bounds and confirmed by the days the hub says are in effect.
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let root: Root;
let host: HTMLDivElement;
let saved: { recording_days?: number; minutes_days?: number } | null;
let writes: Record<string, unknown>[];
/** What a PUT answers instead of the stored state, when set. */
let answer: ((body: Record<string, unknown>) => Response) | undefined;
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status });
const flush = async () => { for (let i = 0; i < 8; i++) await act(async () => { await Promise.resolve(); }); };
/** False: a manager from before minutes_days (#3780), which says only recording_days. */
let withMinutes: boolean;
const effective = () => saved && { recording_days: saved.recording_days, ...(withMinutes ? { minutes_days: Math.max(saved.minutes_days ?? 365, saved.recording_days ?? 14) } : {}) };
const state = () => ({ manager: "assistant", notes: "", ...(effective() ?? {}) });
const input = (label: string) => host.querySelector<HTMLInputElement>(`input[aria-label="${label}"]`);
const recordings = () => input(t("settings-assistantsettings.recording_days"));
const minutes = () => input(t("settings-assistantsettings.minutes_days"));
async function enter(el: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
  await act(async () => { setter.call(el, value); el.dispatchEvent(new Event("input", { bubbles: true })); });
  await act(async () => { el.dispatchEvent(new FocusEvent("focusout", { bubbles: true })); });
  await flush();
}
beforeEach(async () => {
  await initI18n("en");
  history.replaceState(null, "", "/settings?tab=persona");
  resetManagerChoice(); setRosterDefault("assistant");
  vi.mocked(toast).mockClear();
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  saved = { recording_days: 14 }; writes = []; answer = undefined; withMinutes = true;
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input).split("?")[0];
    if (path.endsWith("/assistant")) {
      if (init?.method === "PUT") {
        const body = JSON.parse(String(init.body)); writes.push(body);
        if (answer) return answer(body);
        saved = { ...saved, ...body };
      }
      return json(state());
    }
    if (path.endsWith("/tasks")) return json({ tasks: [] });
    if (path.endsWith("/memory")) return json({ files: [] });
    throw new Error(`Unexpected path: ${path}`);
  }));
});
afterEach(async () => { act(() => root.unmount()); host.remove(); vi.unstubAllGlobals(); });
async function render() { await act(async () => root.render(<AssistantSettings />)); await flush(); }

it("shows both retentions in effect and saves a change the hub confirms", async () => {
  await render();
  expect(host.textContent).toContain("Recordings");
  expect(host.textContent).toContain(t("settings-assistantsettings.recordings_footer"));
  expect(host.textContent).not.toContain(t("settings-assistantsettings.recordings_footer_with_minutes"));
  expect([recordings()!.value, minutes()!.value]).toEqual(["14", "365"]);
  await enter(minutes()!, "730");
  expect(writes).toEqual([{ minutes_days: 730 }]);
  expect(minutes()!.value).toBe("730");
  await enter(recordings()!, "30");
  expect(writes.at(-1)).toEqual({ recording_days: 30 });
  expect(recordings()!.value).toBe("30");
  expect(toast).not.toHaveBeenCalled();
});

it("has no group on a hub that does not say its retention", async () => {
  saved = null;
  await render();
  expect(recordings()).toBeNull();
  expect(minutes()).toBeNull();
  expect(host.textContent).not.toContain(t("settings-assistantsettings.recordings_footer"));
});

it("promises nothing about the minutes on a manager that deletes them with the recording", async () => {
  withMinutes = false;
  await render();
  expect(recordings()!.value).toBe("14");
  expect(minutes()).toBeNull();
  expect(host.textContent).toContain(t("settings-assistantsettings.recordings_footer_with_minutes"));
  expect(host.textContent).not.toContain(t("settings-assistantsettings.recordings_footer"));
  await enter(recordings()!, "30");
  expect(writes).toEqual([{ recording_days: 30 }]);
  expect(recordings()!.value).toBe("30");
  expect(toast).not.toHaveBeenCalled();
});

it("sends nothing out of bounds or not a whole number, and says the bounds", async () => {
  await render();
  for (const [field, value, max] of [[minutes, "0", 3650], [minutes, "3651", 3650], [minutes, "1.5", 3650], [recordings, "366", 365], [recordings, "", 365]] as const) {
    await enter(field()!, value);
    expect(field()!.getAttribute("aria-invalid")).toBe("true");
    expect(host.textContent).toContain(t("settings-assistantsettings.days_range", { min: 1, max }));
    await act(async () => { field()!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })); });
  }
  expect(writes).toEqual([]);
  expect([recordings()!.value, minutes()!.value]).toEqual(["14", "365"]);
});

it("shows the minutes' days in effect when the value asked for is below the recordings'", async () => {
  saved = { recording_days: 60, minutes_days: 90 };
  await render();
  await enter(minutes()!, "7");
  expect(writes).toEqual([{ minutes_days: 7 }]);
  expect(minutes()!.value).toBe("60");
  expect(toast).not.toHaveBeenCalled();
});

it.each([
  ["another value", () => json({ manager: "assistant", recording_days: 14, minutes_days: 365 }), () => t("settings-assistantsettings.retention_unconfirmed")],
  ["no manager identity", () => json({ recording_days: 14, minutes_days: 30 }), () => t("settings-assistantsettings.retention_unconfirmed")],
  ["a refusal", () => json({ error: "Synthetic refusal" }, 400), () => "Synthetic refusal"],
])("keeps what is in effect and says so after %s", async (_, reply, why) => {
  answer = reply;
  await render();
  await enter(minutes()!, "30");
  expect(writes).toEqual([{ minutes_days: 30 }]);
  expect(toast).toHaveBeenCalledWith(expect.stringContaining(why()), "error");
  expect(minutes()!.value).toBe("365");
});
