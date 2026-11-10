// @vitest-environment jsdom
// A lost push is filled even while the agent keeps working (#5355): each gapped push used to restart the 1.5 s fill
// timer, so in a busy turn (a push every second or so) the fill never ran. And a lost push for the last line of a burst
// is asked for by the session's rev, which its updated_at cannot tell (#1179).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ItemsEvent, listenSession } from "../../lib/store";
import type { Item, ItemsPage } from "../../lib/types";

const calls: string[] = [];
type Listener = Parameters<typeof listenSession>[2];
let listener: Listener | undefined;

vi.mock("../../lib/api", async (orig) => ({
  ...(await orig<typeof import("../../lib/api")>()),
  api: vi.fn(async (path: string): Promise<ItemsPage> => {
    calls.push(path);
    if (path.includes("after_seq=")) return { items: [item(101), item(102)], to_seq: 140 } as ItemsPage;
    return { items: [item(100)], to_seq: 100 } as ItemsPage;
  }),
}));
vi.mock("../../lib/store", async (orig) => ({
  ...(await orig<typeof import("../../lib/store")>()),
  send: vi.fn(),
  listenSession: (_mid: string, _sid: string, l: Listener) => {
    listener = l;
    return () => (listener = undefined);
  },
}));

const { useTranscript } = await import("./useTranscript");
const { api } = await import("../../lib/api");

function item(seq: number): Item {
  return { id: `i${seq}`, seq, role: "assistant", kind: "text", text: `row ${seq}`, ts: "2026-11-06T00:00:00Z" } as Item;
}

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let seqs: number[] = [];
function Probe({ sid, updatedAt, rev }: { sid: string; updatedAt?: string; rev?: number }) {
  seqs = useTranscript("m1", sid, { updatedAt, rev }).items.map((i) => i.seq);
  return null;
}

let root: Root;
beforeEach(() => {
  vi.useFakeTimers();
  calls.length = 0;
  root = createRoot(document.createElement("div"));
});
afterEach(() => {
  act(() => root.unmount());
  vi.useRealTimers();
});

const push = (e: ItemsEvent) => act(() => listener!.items!(e));
const fills = () => calls.filter((c) => c.includes("after_seq=")).length;

async function open(sid: string) {
  act(() => root.render(<Probe sid={sid} />));
  await act(async () => {}); // the first page
  expect(seqs).toEqual([100]);
}

describe("a gap in the pushed items", () => {
  it("fills from covered when a pushed batch says more_after", async () => {
    await open("s1");
    push({ from_seq: 100, to_seq: 110, items: [item(110)], more_after: true } as ItemsEvent);
    await act(async () => void vi.advanceTimersByTime(1600));
    expect(fills()).toBe(1);
    expect(calls.find((c) => c.includes("after_seq="))).toContain("after_seq=110");
  });

  it("is filled 1.5 s after it shows, while gapped pushes keep coming", async () => {
    await open("s1");
    // 101-110 were lost; the agent keeps pushing a row every 400 ms.
    for (let n = 0; n < 6; n++) {
      push({ from_seq: 111 + n, to_seq: 111 + n, items: [item(111 + n)] } as ItemsEvent);
      await act(async () => void vi.advanceTimersByTime(400));
    }
    expect(fills()).toBe(1);
    expect(calls.find((c) => c.includes("after_seq="))).toContain("after_seq=100");
    expect(seqs.slice(0, 3)).toEqual([100, 101, 102]);
  });

  it("starts a fresh timer for the next conversation's gap", async () => {
    await open("s1");
    push({ from_seq: 120, to_seq: 120, items: [item(120)] } as ItemsEvent);
    act(() => root.render(<Probe sid="s2" />)); // switched before the fill ran
    await act(async () => {});
    push({ from_seq: 130, to_seq: 130, items: [item(130)] } as ItemsEvent);
    await act(async () => void vi.advanceTimersByTime(1600));
    expect(fills()).toBe(1);
  });
});

describe("a lost last line of a burst (#1179)", () => {
  // Every item is written at T; the list's updated_at moves by less than the 1 s slack, so only its rev tells.
  const at = (ms: number) => new Date(Date.parse("2026-11-06T00:00:00Z") + ms).toISOString();
  const list = (sid: string, ms: number, rev: number) => act(async () => root.render(<Probe sid={sid} updatedAt={at(ms)} rev={rev} />));
  const wait = (ms: number) => act(async () => void vi.advanceTimersByTime(ms));
  beforeEach(() => void vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible"));
  afterEach(() => vi.restoreAllMocks());

  // From #1193, with the rev the list carries.
  it("asks once quiet when the list moved on and no push came, and not again for a push alone", async () => {
    await list("burst-lost", 0, 100);
    await wait(6000);
    expect(fills()).toBe(0); // what the first page read is the baseline
    await list("burst-lost", 300, 110); // a newer list, its items push lost
    await wait(4000);
    expect(fills()).toBe(1);
    expect(calls.find((c) => c.includes("after_seq="))).toContain("after_seq=100");
    expect(seqs).toEqual([100, 101, 102]);

    push({ from_seq: 140, to_seq: 141, items: [item(141)] } as ItemsEvent); // the stream works, the list stays
    await wait(30_000);
    expect(fills()).toBe(1);
  });

  // From #1193: the HTTP read is on its way when the list moves again.
  it("asks again about a rev that moved while an ask was on its way", async () => {
    await list("burst-inflight", 0, 100);
    let finish!: (page: ItemsPage) => void;
    vi.mocked(api).mockImplementationOnce((path) => {
      calls.push(path);
      return new Promise<ItemsPage>((resolve) => void (finish = resolve));
    });
    await list("burst-inflight", 300, 110);
    await wait(6000);
    expect(fills()).toBe(1);
    await list("burst-inflight", 600, 120); // the node read the file before this line was written
    await act(async () => finish({ items: [item(101)], to_seq: 101 } as ItemsPage));
    await wait(12_000);
    expect(fills()).toBe(2);
    expect(calls.filter((c) => c.includes("after_seq="))[1]).toContain("after_seq=101");
  });

  // From the review of #1193: the list is the node's poll and the push its transcript watch, so a list can come first.
  it("asks for a lost last line when the newer list came before the previous line's push", async () => {
    await list("burst-reordered", 0, 100);
    await wait(6000);
    expect(fills()).toBe(0);
    await list("burst-reordered", 600, 120); // lines 101 and 102 written
    push({ from_seq: 100, to_seq: 101, items: [item(101)] } as ItemsEvent); // 101's push, no gap; 102's is lost
    expect(fills()).toBe(0);
    await wait(12_000);
    expect(fills()).toBe(1);
    expect(seqs).toEqual([100, 101, 102]);
  });

  it("hears the hub's revs event, which moves the rev without a new list", async () => {
    await list("burst-revs", 0, 100);
    await wait(6000);
    push({ from_seq: 100, to_seq: 101, items: [item(101)] } as ItemsEvent);
    act(() => listener!.rev?.(120)); // 101 and 102 written; 102's push is lost
    await wait(4000);
    expect(fills()).toBe(1);
    expect(seqs).toEqual([100, 101, 102]);
    await wait(30_000);
    expect(fills()).toBe(1); // one ask per move
  });
});
