// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { api, credentialOwner, getToken, handleUnauthorized, setToken } from "./api";

const unauthorized = vi.fn();
let respond: (response: Response) => void;

beforeEach(() => {
  localStorage.clear();
  setToken("synthetic-current-token");
  unauthorized.mockClear();
  handleUnauthorized(unauthorized);
  vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>((resolve) => { respond = resolve; })));
});

afterEach(() => {
  handleUnauthorized(() => {});
  vi.unstubAllGlobals();
});

it.each([
  { mode: "ordinary", expected: 1 },
  { mode: "owned", expected: 1 },
  { mode: "probe", expected: 0 },
  { mode: "different-token", expected: 0 },
  { mode: "retired-generation", expected: 0 },
])("401 recovery preserves the $mode credential contract", async ({ mode, expected }) => {
  const token = getToken(), tokenOwner = credentialOwner();
  const init = mode === "ordinary" ? {} : mode === "probe" ? { token } : {
    token: mode === "different-token" ? "synthetic-probe-token" : token, tokenOwner,
  };
  const result = api("/machines", init).catch((error: Error) => error);
  if (mode === "retired-generation") {
    setToken("synthetic-other-token");
    setToken(token);
  }
  respond(new Response(JSON.stringify({ error: "invalid token" }), { status: 401, headers: { "Content-Type": "application/json" } }));
  expect((await result as Error).message).toBe("invalid token");
  expect(unauthorized).toHaveBeenCalledTimes(expected);
});
