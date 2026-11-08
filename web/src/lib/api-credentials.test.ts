// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { api, credentialOwner, handleUnauthorized, setToken } from "./api";

const unauthorized = vi.fn<() => void>();
const denied = () => new Response(JSON.stringify({ error: "Synthetic unauthorized request" }), { status: 401 });
beforeEach(() => {
  setToken("example-current-token"); unauthorized.mockClear(); handleUnauthorized(unauthorized);
  vi.stubGlobal("fetch", vi.fn(async () => denied()));
});
afterEach(() => { setToken(""); handleUnauthorized(() => {}); vi.unstubAllGlobals(); });

it("recovers an implicit current-token 401 once", async () => {
  await expect(api("/assistant")).rejects.toMatchObject({ status: 401 });
  expect(unauthorized).toHaveBeenCalledTimes(1);
});
it("recovers an explicitly owned current-token 401 once", async () => {
  await expect(api("/assistant", { token: "example-current-token", tokenOwner: credentialOwner() })).rejects.toMatchObject({ status: 401 });
  expect(unauthorized).toHaveBeenCalledTimes(1);
});
it("does not sign out a replacement credential after a retired request returns 401", async () => {
  let finish!: (response: Response) => void;
  vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>((resolve) => { finish = resolve; })));
  const pending = api("/assistant", { token: "example-current-token", tokenOwner: credentialOwner() });
  setToken("example-replacement-token"); finish(denied());
  await expect(pending).rejects.toMatchObject({ status: 401 });
  expect(unauthorized).not.toHaveBeenCalled();
});
it("does not let a different explicit probe token sign out the current owner", async () => {
  await expect(api("/assistant", { token: "example-probe-token", tokenOwner: credentialOwner() })).rejects.toMatchObject({ status: 401 });
  expect(new Headers(vi.mocked(fetch).mock.calls[0][1]?.headers).get("Authorization")).toBe("Bearer example-probe-token");
  expect(unauthorized).not.toHaveBeenCalled();
});
it("preserves explicit-token opt-out when no owner was supplied", async () => {
  await expect(api("/assistant", { token: "example-current-token" })).rejects.toMatchObject({ status: 401 });
  expect(unauthorized).not.toHaveBeenCalled();
});
