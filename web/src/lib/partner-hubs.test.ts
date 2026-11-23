// @vitest-environment jsdom
// Partner hubs (lib/partner-hubs.ts): reading a pasted code, the state's words, the line under the name, the "Ask me before
// sending" default, the refusals in plain words, the routes each change goes to, and that the other hub refusing a code
// (401) does not sign this page out.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, ApiError, handleUnauthorized, setToken } from "./api";
import { chooseManager, MANAGER_HEADER, resetManagerChoice, setRosterDefault } from "./manager-choice";
import {
  confirmFor, listPartnerHubs, listPartnerPrefs, makePartnerCode, pairPartnerHub, parsePartnerCode, partnerError, partnerLine,
  partnerStatus, setPartnerConfirm, setPartnerDial, unpairPartnerHub, withPref, type PartnerHub,
} from "./partner-hubs";

const SECRET = `jh_${"ab12".repeat(16)}`;
const FP = "Q2hlY2tUaGlzRmluZ2VycHJpbnRBZ2FpbnN0VGhlSHVi";
const code = (extra = "") => `persona://partner?hub=hub-b&name=Home+hub&fp=${FP}&tls=18662&url=https%3A%2F%2Fhub.example.com%3A18662&code=${SECRET}${extra}`;
const hub = (extra: Partial<PartnerHub> = {}): PartnerHub => ({ hub_id: "hub-b", name: "Home hub", fp: FP, state: "connected", dial: "auto", created_at: "2026-11-20T08:00:00Z", ...extra });
const NOW = Date.parse("2026-11-23T10:00:00Z");

describe("a pasted pairing code", () => {
  it("names the hub and its fingerprint", () => {
    expect(parsePartnerCode(code())).toEqual({ hub_id: "hub-b", name: "Home hub", fp: FP, code: code() });
  });

  it("ignores the spaces and line breaks a copy adds, and sends the code without them", () => {
    const wrapped = `  ${code().slice(0, 40)}\n${code().slice(40)}  `;
    expect(parsePartnerCode(wrapped)?.code).toBe(code());
  });

  it("falls back to the hub id when the code has no name", () => {
    expect(parsePartnerCode(code().replace("name=Home+hub&", ""))?.name).toBe("hub-b");
  });

  it.each([
    ["empty", ""],
    ["not a link", "jh_abc"],
    ["a mesh link code", code().replace("://partner", "://peer")],
    ["another scheme", code().replace("persona://", "https://")],
    ["no fingerprint", code().replace(`fp=${FP}&`, "")],
    ["no hub", code().replace("hub=hub-b&", "")],
    ["a cut-off secret", code().slice(0, -4)],
    ["another credential", code().replace(SECRET, `jx_${"ab12".repeat(16)}`)],
  ])("is not one when it is %s", (_, text) => {
    expect(parsePartnerCode(text)).toBeNull();
  });
});

describe("a partner's state", () => {
  it("has a pill for each state, an unknown one reading as offline", () => {
    expect(partnerStatus(hub())).toEqual({ label: "已连接", tone: "green" });
    expect(partnerStatus(hub({ state: "connecting" }))).toEqual({ label: "正在连接", tone: "accent" });
    expect(partnerStatus(hub({ state: "refused" }))).toEqual({ label: "连不上", tone: "red" });
    expect(partnerStatus(hub({ state: "offline" }))).toEqual({ label: "离线" });
    expect(partnerStatus(hub({ state: "later" }))).toEqual({ label: "离线" });
  });

  it("says since when it is connected, when it last was, or what this hub waits for", () => {
    expect(partnerLine(hub({ since: "2026-11-23T07:00:00Z" }), NOW)).toBe("3小时前连上");
    expect(partnerLine(hub({ state: "offline", dial: "never", last_seen: "2026-11-22T10:00:00Z" }), NOW)).toBe("上次连接：昨天");
    expect(partnerLine(hub({ state: "offline", dial: "never" }), NOW)).toBe("等它连到这台 hub");
    expect(partnerLine(hub({ state: "connecting" }), NOW)).toBe("还没连上过");
  });
});

describe("Ask me before sending", () => {
  it("is on for a hub with no stored value, and unknown until the prefs are read", () => {
    const prefs = [{ hub_id: "hub-c", confirm: false }];
    expect(confirmFor(null, "hub-b")).toBeUndefined();
    expect(confirmFor(prefs, "hub-b")).toBe(true);
    expect(confirmFor(prefs, "hub-c")).toBe(false);
    expect(confirmFor(withPref(prefs, { hub_id: "hub-c", confirm: true }), "hub-c")).toBe(true);
    expect(withPref(null, { hub_id: "hub-b", confirm: false })).toEqual([{ hub_id: "hub-b", confirm: false }]);
  });
});

describe("a refused change", () => {
  it.each([
    ["own_code", 409, "这是这台 hub 自己的配对码"],
    ["already_linked", 409, "同一个互联的 hub 组"],
    ["no_tls", 409, "不能生成配对码"],
    ["partners_off", 403, "这台 hub 关闭了协作 hub"],
    // the other hub's refusal of the pairing comes back as this hub's 409 with the other hub's code
    ["partners_off", 409, "对方 hub 关闭了协作 hub"],
    ["code_refused", 401, "已经用过或失效"],
  ])("%s (%i) reads in the page's words", (codeName, status, words) => {
    expect(partnerError(new ApiError(status, "hub text", { error: "hub text", code: codeName }))).toContain(words);
  });

  it("leaves to the hub's own sentence what only it knows: which hub refused, and why a code does not read", () => {
    // this hub's own member_hub and the other hub's look alike: the hub's sentence says whose it is
    const other = "对方 hub 拒绝了：这台 hub 已加入互联的 hub 组";
    expect(partnerError(new ApiError(409, other, { error: other, code: "member_hub" }))).toBe(other);
    expect(partnerError(new ApiError(409, other, { error: other, code: "pin_changed" }))).toBe(other);
    expect(partnerError(new ApiError(400, "配对码里没有对方的加密地址", { error: "配对码里没有对方的加密地址", code: "bad_code" }))).toBe("配对码里没有对方的加密地址");
    // without a sentence, the page's own
    expect(partnerError(new ApiError(409, "HTTP 409", { code: "member_hub" }))).toContain("已加入互联的 hub 组");
    expect(partnerError(new ApiError(409, "HTTP 409", { code: "pin_changed" }))).toContain("指纹变了");
    expect(partnerError(new ApiError(400, "HTTP 400", { code: "bad_code" }))).toContain("读不出来");
  });

  it("does not blame the code or the other hub for this page's own refusals", () => {
    // this page's credential refused: not "the other hub refused this code"
    expect(partnerError(new ApiError(401, "invalid token", { error: "invalid token", code: "invalid_token" }))).toBe("invalid token");
    // a bad dial value: not "this code can't be read"
    expect(partnerError(new ApiError(400, "dial 只能是 auto 或 never", { error: "dial 只能是 auto 或 never", code: "bad_request" }))).toBe("dial 只能是 auto 或 never");
    // a proxy's 502 page in front of this hub: this hub is unavailable, not the other one
    expect(partnerError(new ApiError(502, "HTTP 502", {}, false))).not.toContain("对方");
  });

  it("says an unreachable hub plainly, with how it failed, and the other hub's own answer as it gave it", () => {
    expect(partnerError(new ApiError(502, "dial tcp 192.0.2.7:18662: i/o timeout", { error: "dial tcp 192.0.2.7:18662: i/o timeout", code: "unreachable" })))
      .toBe("连不上对方 hub（dial tcp 192.0.2.7:18662: i/o timeout）。请确认它在运行、从这台 hub 能访问到，然后再试一次。");
    expect(partnerError(new ApiError(502, "HTTP 502", { code: "unreachable" }))).toContain("连不上对方 hub");
    expect(partnerError(new ApiError(502, "连上的是 hub x，不是配对码里的 y", { error: "连上的是 hub x，不是配对码里的 y", code: "wrong_hub" }))).toBe("连上的是 hub x，不是配对码里的 y");
  });
});

describe("the routes", () => {
  let calls: { url: string; method: string; body?: unknown }[];
  let reply: Response;
  const unauthorized = vi.fn();
  beforeEach(() => {
    calls = [];
    reply = Response.json({ ok: true });
    setToken("synthetic-token");
    unauthorized.mockClear();
    handleUnauthorized(unauthorized);
    vi.stubGlobal("fetch", vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(url), method: init?.method ?? "GET", body: init?.body ? JSON.parse(String(init.body)) : undefined });
      return reply.clone();
    }));
  });
  afterEach(() => {
    handleUnauthorized(() => {});
    vi.unstubAllGlobals();
    setToken("");
    resetManagerChoice();
  });

  it("puts the pairing on the hub's routes and the confirmation on the default manager's, whichever assistant is picked", async () => {
    setRosterDefault("assistant");
    chooseManager("work");
    await listPartnerHubs();
    await makePartnerCode();
    await pairPartnerHub(code());
    await setPartnerDial("hub b", "never");
    await unpairPartnerHub("hub-b");
    await listPartnerPrefs();
    await setPartnerConfirm("hub-b", false);
    expect(calls.map((c) => `${c.method} ${c.url.replace(/^.*\/api\/v1/, "")}`)).toEqual([
      "GET /partner-hubs", "POST /partner-hubs/code", "POST /partner-hubs", "PATCH /partner-hubs/hub%20b", "DELETE /partner-hubs/hub-b",
      "GET /partner-messages/prefs", "PUT /partner-messages/prefs/hub-b",
    ]);
    expect(calls.map((c) => c.body)).toEqual([undefined, undefined, { code: code() }, { dial: "never" }, undefined, undefined, { confirm: false }]);
    const headers = () => (vi.mocked(fetch).mock.calls as [unknown, RequestInit][]).map(([, init]) => new Headers(init.headers).get(MANAGER_HEADER));
    expect(headers()).toEqual(Array(7).fill(null));
    // while an assistant's own route carries the picked one
    await api("/assistant/settings");
    expect(headers().at(-1)).toBe("work");
  });

  it("does not sign the page out when the other hub refuses the code", async () => {
    reply = Response.json({ error: "配对码已失效", code: "code_refused" }, { status: 401 });
    await expect(pairPartnerHub(code())).rejects.toMatchObject({ status: 401 });
    expect(unauthorized).not.toHaveBeenCalled();
    // any other route's 401 still does
    await expect(listPartnerHubs()).rejects.toMatchObject({ status: 401 });
    expect(unauthorized).toHaveBeenCalledTimes(1);
  });
});
