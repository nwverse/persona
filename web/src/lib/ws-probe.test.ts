import { describe, expect, it, vi } from "vitest";
import { connFacts, NO_LINK } from "./connection";
import { probeEdge } from "./edge-auth";

describe("WS rejected-upgrade HTTP error probe", () => {
  it("surfaces 5xx JSON error detail via onServerError", async () => {
    const onServerError = vi.fn();
    const fetcher = vi.fn(async () =>
      new Response(JSON.stringify({ error: "hub maintenance in progress" }), {
        status: 503,
        statusText: "Service Unavailable",
        headers: { "Content-Type": "application/json" },
      }),
    ) as unknown as typeof fetch;

    await probeEdge(fetcher, undefined, onServerError);
    expect(onServerError).toHaveBeenCalledWith("hub maintenance in progress");
  });

  it("falls back to HTTP status text for non-JSON 5xx responses", async () => {
    const onServerError = vi.fn();
    const fetcher = vi.fn(async () =>
      new Response("bad gateway", { status: 502, statusText: "Bad Gateway" }),
    ) as unknown as typeof fetch;

    await probeEdge(fetcher, undefined, onServerError);
    expect(onServerError).toHaveBeenCalledWith("HTTP 502 Bad Gateway");
  });

  it("prepends lastError in connFacts when unreachable/connecting and hides it when online or offline-network", () => {
    const now = 10_000;
    const link = { ...NO_LINK, nextTry: now + 3000, lastError: "HTTP 502 Bad Gateway" };
    expect(connFacts("unreachable", link, now)[0]).toBe("HTTP 502 Bad Gateway");
    expect(connFacts("connecting", link, now)[0]).toBe("HTTP 502 Bad Gateway");
    expect(connFacts("no_network", link, now)).toEqual([]);
    expect(connFacts("online", link, now)).toEqual([]);
  });
});
