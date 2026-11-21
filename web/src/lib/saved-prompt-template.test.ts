import { describe, expect, it } from "vitest";
import { parseSavedPromptTemplate as parse, renderSavedPromptTemplate as render } from "./saved-prompt-template";

describe("saved prompt templates", () => {
  it("retains first-use order, Unicode names and case-sensitive repeated fields", () => {
    const template = parse("{{ \t主题-２ \t}} / {{reviewer}} / {{主题-２}} / {{Reviewer}} / {{_a\u0301}}");
    expect(template.fields).toEqual(["主题-２", "reviewer", "Reviewer", "_a\u0301"]);
    expect(render(template, new Map([["主题-２", "状态"], ["reviewer", "Ada"], ["Reviewer", "Lin"]])))
      .toEqual({ text: "状态 / Ada / 状态 / Lin / " });
  });

  it("unescapes only defined text sequences and inserts values literally in a single pass", () => {
    const template = parse("\\{{json\\}} {x} \\path \\\\ {{field}} {{field}}");
    expect(template.fields).toEqual(["field"]);
    const value = "\\{{field}} /permissions\nC:\\repo";
    expect(render(template, new Map([["field", value]]))).toEqual({ text: "{{json}} {x} \\path \\ " + value + " " + value });
  });

  it.each([
    "{{}}", "{{1x}}", "{{x y}}", "{{x\n}}", "{{x\r}}", "{{ \u00a0x}}",
    "{{x", "}} {{x}}", "{{x}} }}", "{{{x}}", "{{x\\}}", "{{" + "a".repeat(33) + "}}",
  ])("rejects invalid syntax: %j", (source) => {
    expect(parse(source)).toMatchObject({ problem: "invalid_syntax", fields: [] });
  });

  it("bounds source size, field names, distinct fields and slots by the specified counts", () => {
    const name = "𐐀".repeat(32);
    expect(parse("{{" + name + "}}").fields).toEqual([name]);
    expect(parse("{{" + name + "a}}").problem).toBe("invalid_syntax");
    expect(parse("{{a}}".repeat(64)).fields).toEqual(["a"]);
    expect(parse("{{a}}".repeat(65)).problem).toBe("too_many_slots");
    const fields = Array.from({ length: 8 }, (_, index) => "{{a" + index + "}}").join("");
    expect(parse(fields).fields).toHaveLength(8);
    expect(parse(fields + "{{b}}").problem).toBe("too_many_fields");
    expect(parse("😀".repeat(7995) + "{{a}}").fields).toEqual(["a"]);
    expect(parse("😀".repeat(7996) + "{{a}}").problem).toBe("source_too_long");
    expect(parse("\\{{plain\\}}").problem).toBe("no_fields");
  });

  it("bounds Unicode field values and repeated expansion without transforming them", () => {
    const accepted = "🙂".repeat(2000);
    const values = new Map([["a", accepted]]);
    expect(render(parse("{{a}}".repeat(16)), values)).toEqual({ text: accepted.repeat(16) });
    expect(render(parse("{{a}}".repeat(16) + "!"), values)).toEqual({ problem: "output_too_long" });
    expect(render(parse("{{a}}"), new Map([["a", accepted + "x"]])))
      .toEqual({ problem: "value_too_long", field: "a" });
  });
});
