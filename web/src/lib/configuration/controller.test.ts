// A configuration form's state over an injected source (controller.ts) and the HTTP source (http.ts): nothing saved
// before the source commits, drafts that survive failures and conflicts, late answers from a replaced source or an
// older read ignored, partial updates and resets, field errors, versions it can't write, and changes made elsewhere.
import { describe, expect, it, vi } from "vitest";
import example from "./fixtures/all-types.json";
import { ConfigurationController } from "./controller";
import { ConfigurationError, type ConfigurationSource, type ConfigurationUpdate } from "./contract";
import { httpConfigurationSource, type ConfigurationRequest } from "./http";

type Doc = typeof example;
const clone = (d: Doc): Doc => JSON.parse(JSON.stringify(d));

/** A source whose answers the test releases one by one. */
function manual(key = "example") {
  const reads: { resolve: (v: unknown) => void; reject: (e: unknown) => void }[] = [];
  const updates: { body: ConfigurationUpdate; resolve: (v: unknown) => void; reject: (e: unknown) => void }[] = [];
  let changed: (() => void) | null = null;
  const source: ConfigurationSource = {
    key,
    read: () => new Promise((resolve, reject) => reads.push({ resolve, reject })),
    update: (body) => new Promise((resolve, reject) => updates.push({ body, resolve, reject })),
    subscribe: (l) => {
      changed = l;
      return () => {
        changed = null;
      };
    },
  };
  return { source, reads, updates, change: () => changed?.(), get subscribed() { return changed !== null; } };
}

const flush = () => new Promise((r) => setTimeout(r, 0));

/** Commits what an update asks on `doc` (the way a writer would), as a new revision. */
function commit(doc: Doc, u: ConfigurationUpdate): Doc {
  const next = clone(doc);
  const fields = (next.groups as { fields: { id: string; default: unknown }[] }[]).flatMap((g) => g.fields);
  Object.assign(next.values, u.changes ?? {});
  for (const id of u.reset ?? []) (next.values as Record<string, unknown>)[id] = fields.find((f) => f.id === id)!.default;
  next.revision = `${doc.revision}+`;
  return next;
}

async function ready(m = manual(), doc: unknown = example) {
  const ctl = new ConfigurationController(m.source);
  ctl.start();
  m.reads[0].resolve(doc);
  await flush();
  return { ctl, m };
}

describe("reading", () => {
  it("loads, then is ready with the document", async () => {
    const m = manual();
    const ctl = new ConfigurationController(m.source);
    ctl.start();
    expect(ctl.snapshot().phase).toBe("loading");
    m.reads[0].resolve(example);
    await flush();
    expect(ctl.snapshot()).toMatchObject({ phase: "ready", writable: true, readError: null });
    expect(ctl.snapshot().doc?.revision).toBe("r1");
  });

  it("says why it couldn't read, and reads again on Retry", async () => {
    const m = manual();
    const ctl = new ConfigurationController(m.source);
    ctl.start();
    m.reads[0].reject(new ConfigurationError("forbidden", "Not yours"));
    await flush();
    expect(ctl.snapshot()).toMatchObject({ phase: "failed", readError: { code: "forbidden", message: "Not yours" } });
    ctl.reload();
    expect(ctl.snapshot().phase).toBe("loading");
    m.reads[1].resolve(example);
    await flush();
    expect(ctl.snapshot().phase).toBe("ready");
  });

  it("refuses something that isn't a document", async () => {
    const { ctl } = await ready(manual(), { hello: 1 });
    expect(ctl.snapshot()).toMatchObject({ phase: "failed", readError: { code: "invalid_document" } });
  });

  it("a disposed form (its source replaced) ignores a late answer", async () => {
    const m = manual();
    const ctl = new ConfigurationController(m.source);
    ctl.start();
    const seen = vi.fn();
    ctl.subscribe(seen);
    ctl.dispose();
    expect(m.subscribed).toBe(false);
    m.reads[0].resolve(example);
    await flush();
    expect(ctl.snapshot().phase).toBe("loading");
    expect(seen).not.toHaveBeenCalled();
  });

  it("an older read that answers after a newer one is ignored", async () => {
    const { ctl, m } = await ready();
    m.change(); // read #2
    ctl.reload(); // read #3
    const newer = { ...clone(example), revision: "r3" };
    m.reads[2].resolve(newer);
    await flush();
    m.reads[1].resolve({ ...clone(example), revision: "r2-old" });
    await flush();
    expect(ctl.snapshot().doc?.revision).toBe("r3");
  });

  it("keeps the document and says so when reading again fails", async () => {
    const { ctl, m } = await ready();
    m.change();
    m.reads[1].reject(new Error("offline"));
    await flush();
    expect(ctl.snapshot()).toMatchObject({ phase: "ready", readError: { code: "failed" } });
    expect(ctl.snapshot().doc?.revision).toBe("r1");
  });
});

describe("writing a choice", () => {
  it("sends only that field against the revision, shows it pending, and saved only once committed", async () => {
    const { ctl, m } = await ready();
    ctl.choose("size", "l");
    expect(m.updates[0].body).toEqual({ schema_version: 1, id: "example", revision: "r1", changes: { size: "l" } });
    expect(ctl.snapshot().busy).toBe("size");
    expect(ctl.snapshot().fields.size).toEqual({ pending: "l" });
    expect(ctl.snapshot().doc?.values.size).toBe("m"); // nothing changed before the source said so
    m.updates[0].resolve(commit(example, m.updates[0].body));
    await flush();
    expect(ctl.snapshot().busy).toBeNull();
    expect(ctl.snapshot().fields.size).toEqual({ saved: true });
    expect(ctl.snapshot().doc?.values.size).toBe("l");
    expect(ctl.snapshot().doc?.revision).toBe("r1+");
  });

  it("puts back what the document says when the write fails, and says why", async () => {
    const { ctl, m } = await ready();
    ctl.choose("enabled", false);
    m.updates[0].reject(new ConfigurationError("storage_failed", "Couldn't keep it"));
    await flush();
    expect(ctl.snapshot().fields.enabled).toEqual({ error: { code: "storage_failed", message: "Couldn't keep it" } });
    expect(ctl.snapshot().doc?.values.enabled).toBe(true);
  });

  it("writes one thing at a time", async () => {
    const { ctl, m } = await ready();
    ctl.choose("size", "s");
    ctl.choose("region", "us");
    expect(m.updates).toHaveLength(1);
    expect(ctl.canWrite("region")).toBe(false);
  });

  it("checks a choice before sending it (duplicates, unknown choices, counts)", async () => {
    const { ctl, m } = await ready();
    ctl.choose("days", ["mon", "mon"]);
    expect(ctl.snapshot().fields.days.error).toEqual({ code: "duplicate" });
    ctl.choose("days", []);
    expect(ctl.snapshot().fields.days.error).toEqual({ code: "too_few_selected" });
    ctl.choose("size", "xl");
    expect(ctl.snapshot().fields.size.error).toEqual({ code: "invalid_choice" });
    expect(m.updates).toHaveLength(0);
  });

  it("never writes a read-only, disabled or unsupported field", async () => {
    const { ctl, m } = await ready();
    ctl.choose("locked", false);
    ctl.choose("paused", true);
    ctl.choose("future", "#000000");
    expect(m.updates).toHaveLength(0);
  });

  it("never writes a document of a version it doesn't know", async () => {
    const { ctl, m } = await ready(manual(), { ...clone(example), schema_version: 2 });
    expect(ctl.snapshot().writable).toBe(false);
    ctl.choose("size", "s");
    ctl.reset("size");
    expect(m.updates).toHaveLength(0);
  });
});

describe("typed text", () => {
  it("is a draft until saved, checked first, and gone once committed", async () => {
    const { ctl, m } = await ready();
    ctl.setDraft("nickname", "x".repeat(21));
    ctl.saveDraft("nickname");
    expect(ctl.snapshot().fields.nickname.error).toEqual({ code: "too_long" });
    expect(m.updates).toHaveLength(0);
    ctl.setDraft("nickname", "Sam 👋");
    expect(ctl.snapshot().fields.nickname).toEqual({ draft: "Sam 👋" });
    ctl.saveDraft("nickname");
    expect(m.updates[0].body.changes).toEqual({ nickname: "Sam 👋" });
    m.updates[0].resolve(commit(example, m.updates[0].body));
    await flush();
    expect(ctl.snapshot().fields.nickname).toEqual({ saved: true });
    expect(ctl.snapshot().doc?.values.nickname).toBe("Sam 👋");
  });

  it("keeps the draft through a failed save", async () => {
    const { ctl, m } = await ready();
    ctl.setDraft("note", "New words");
    ctl.saveDraft("note");
    m.updates[0].reject(new Error("network"));
    await flush();
    expect(ctl.snapshot().fields.note).toEqual({ draft: "New words", error: { code: "failed" } });
  });

  it("keeps the draft through a conflict, reads again only when asked, and still has it after", async () => {
    const { ctl, m } = await ready();
    ctl.setDraft("note", "Mine");
    ctl.saveDraft("note");
    m.updates[0].reject(new ConfigurationError("conflict"));
    await flush();
    expect(ctl.snapshot().conflict).toBe(true);
    expect(m.reads).toHaveLength(1); // nothing re-read on its own
    expect(ctl.snapshot().fields.note).toEqual({ draft: "Mine" });
    ctl.reload();
    m.reads[1].resolve({ ...clone(example), revision: "r2", values: { ...example.values, note: "Theirs" } });
    await flush();
    expect(ctl.snapshot().conflict).toBe(false);
    expect(ctl.snapshot().doc?.values.note).toBe("Theirs");
    expect(ctl.snapshot().fields.note).toEqual({ draft: "Mine" });
  });

  it("keeps a draft when the resource changes elsewhere", async () => {
    const { ctl, m } = await ready();
    ctl.setDraft("nickname", "Typing");
    m.change();
    m.reads[1].resolve({ ...clone(example), revision: "r2" });
    await flush();
    expect(ctl.snapshot().doc?.revision).toBe("r2");
    expect(ctl.snapshot().fields.nickname).toEqual({ draft: "Typing" });
  });

  it("puts field errors on the fields they name", async () => {
    const { ctl, m } = await ready();
    ctl.setDraft("nickname", "taken");
    ctl.saveDraft("nickname");
    m.updates[0].reject(new ConfigurationError("invalid_update", "", [{ id: "nickname", code: "taken", message: "Someone has that name" }]));
    await flush();
    expect(ctl.snapshot().fields.nickname).toEqual({ draft: "taken", error: { code: "taken", message: "Someone has that name" } });
  });

  it("numbers: parsed and checked as the field says; anything else stays a draft with why", async () => {
    const { ctl, m } = await ready();
    ctl.setDraft("limit", "2.5");
    ctl.saveDraft("limit");
    expect(ctl.snapshot().fields.limit.error).toEqual({ code: "not_integer" });
    ctl.setDraft("limit", "abc");
    ctl.saveDraft("limit");
    expect(ctl.snapshot().fields.limit.error).toEqual({ code: "out_of_range" });
    ctl.setDraft("limit", "");
    ctl.saveDraft("limit");
    expect(ctl.snapshot().fields.limit.error).toEqual({ code: "invalid_type" });
    ctl.setDraft("ratio", "1.75");
    ctl.saveDraft("ratio");
    expect(m.updates[0].body.changes).toEqual({ ratio: 1.75 });
  });

  it("Discard drops the draft", async () => {
    const { ctl } = await ready();
    ctl.setDraft("nickname", "Oops");
    ctl.discard("nickname");
    expect(ctl.snapshot().fields.nickname).toBeUndefined();
  });
});

describe("resetting", () => {
  it("sends reset for that field only", async () => {
    const { ctl, m } = await ready();
    ctl.reset("limit");
    expect(m.updates[0].body).toEqual({ schema_version: 1, id: "example", revision: "r1", reset: ["limit"] });
    m.updates[0].resolve(commit(example, m.updates[0].body));
    await flush();
    expect(ctl.snapshot().doc?.values.limit).toBe(10);
    expect(ctl.snapshot().doc?.values.mode).toBe("manual"); // the rest untouched
  });
});

describe("a change made elsewhere during a write", () => {
  it("reads again once the write is done", async () => {
    const { ctl, m } = await ready();
    ctl.choose("size", "l");
    m.change();
    expect(m.reads).toHaveLength(1);
    m.updates[0].resolve(commit(example, m.updates[0].body));
    await flush();
    expect(m.reads).toHaveLength(2);
    expect(ctl.snapshot().busy).toBeNull();
  });
});

describe("the HTTP source", () => {
  function http(answer: (r: ConfigurationRequest) => { status: number; body: unknown }) {
    const calls: [string, ConfigurationRequest][] = [];
    const source = httpConfigurationSource({
      endpoint: "/api/v1/example/settings",
      transport: async (endpoint, request) => {
        calls.push([endpoint, request]);
        return answer(request);
      },
    });
    return { source, calls };
  }

  it("GETs the endpoint for the document and PATCHes it with the update; its key is the endpoint", async () => {
    const { source, calls } = http((r) => ({ status: 200, body: r.method === "GET" ? example : commit(example, JSON.parse(r.body!)) }));
    expect(source.key).toBe("/api/v1/example/settings");
    await expect(source.read()).resolves.toEqual(example);
    const update = { schema_version: 1, id: "example", revision: "r1", changes: { size: "l" } };
    const doc = (await source.update(update)) as Doc;
    expect(doc.values.size).toBe("l");
    expect(calls.map(([e, r]) => [e, r.method, r.body ? JSON.parse(r.body) : undefined])).toEqual([
      ["/api/v1/example/settings", "GET", undefined],
      ["/api/v1/example/settings", "PATCH", update],
    ]);
  });

  it("turns an error answer into a ConfigurationError (409 is a conflict even without a code)", async () => {
    const { source } = http((r) => (r.method === "GET" ? { status: 403, body: { error: "No", code: "forbidden" } } : { status: 409, body: undefined }));
    await expect(source.read()).rejects.toMatchObject({ code: "forbidden", message: "No" });
    await expect(source.update({ schema_version: 1, id: "example", revision: "r1" })).rejects.toMatchObject({ code: "conflict" });
  });

  it("a form over it treats an answer for another resource as invalid", async () => {
    const { source } = http((r) => ({ status: 200, body: r.method === "GET" ? example : { ...example, id: "other" } }));
    const ctl = new ConfigurationController(source);
    ctl.start();
    await flush();
    ctl.choose("size", "l");
    await flush();
    expect(ctl.snapshot().fields.size.error).toEqual({ code: "invalid_document" });
    expect(ctl.snapshot().doc?.id).toBe("example");
  });
});
