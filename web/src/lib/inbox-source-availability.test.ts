import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { inboxSourceVisibility } from "./inbox-source-availability";

interface PresenceCase {
  name: string;
  input: { offers: { lark: boolean; mail: boolean }; stored_sources?: { lark: boolean; mail: boolean } };
  expected: { lark: boolean; mail: boolean };
}

const fixture = JSON.parse(readFileSync(new URL("../../../spec/fixtures/inbox-sources/presence.json", import.meta.url), "utf8")) as { cases: PresenceCase[] };

describe("Inbox source visibility (#17399)", () => {
  for (const c of fixture.cases) {
    it(c.name, () => {
      expect(inboxSourceVisibility(c.input.offers, c.input.stored_sources)).toEqual(c.expected);
    });
  }

  it("treats an explicit merged null as unknown, not an empty archive", () => {
    expect(inboxSourceVisibility({ lark: false, mail: false }, null)).toEqual({ lark: true, mail: true });
  });
});
