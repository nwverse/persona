const SOURCE_MAX = 8_000;
const FIELDS_MAX = 8;
const SLOTS_MAX = 64;
export const VALUE_MAX = 2_000;
const OUTPUT_MAX = 32_000;
const FIELD = /^[\p{L}_][\p{L}\p{Nd}\p{M}_-]{0,31}(?![\s\S])/u;

type ParseProblem = "invalid_syntax" | "no_fields" | "too_many_fields" | "too_many_slots" | "source_too_long";
type Part = string | { field: string };
export type SavedPromptTemplate =
  | { fields: readonly string[]; parts: readonly Part[]; problem?: never }
  | { fields: readonly []; parts?: never; problem: ParseProblem };

function countPoints(text: string): number {
  let count = 0;
  for (const _ of text) count++;
  return count;
}

export function parseSavedPromptTemplate(text: string): SavedPromptTemplate {
  if (countPoints(text) > SOURCE_MAX) return { fields: [], problem: "source_too_long" };
  const fields = new Set<string>();
  const parts: Part[] = [];
  let slots = 0;
  let literal = "";
  for (let at = 0; at < text.length;) {
    if (text[at] === "\\") {
      const escaped = text.slice(at + 1, at + 3);
      if (escaped === "{{" || escaped === "}}") {
        literal += escaped;
        at += 3;
        continue;
      }
      if (text[at + 1] === "\\") at++;
    } else if (text.startsWith("}}", at)) {
      return { fields: [], problem: "invalid_syntax" };
    } else if (text.startsWith("{{", at)) {
      const end = text.indexOf("}}", at + 2);
      if (end === -1) return { fields: [], problem: "invalid_syntax" };
      const name = text.slice(at + 2, end).replace(/^[ \t]+|[ \t]+$/g, "");
      if (!FIELD.test(name)) return { fields: [], problem: "invalid_syntax" };
      fields.add(name);
      if (fields.size > FIELDS_MAX) return { fields: [], problem: "too_many_fields" };
      if (++slots > SLOTS_MAX) return { fields: [], problem: "too_many_slots" };
      if (literal) parts.push(literal);
      literal = "";
      parts.push({ field: name });
      at = end + 2;
      continue;
    }
    literal += text[at++];
  }
  if (!slots) return { fields: [], problem: "no_fields" };
  if (literal) parts.push(literal);
  return { fields: [...fields], parts };
}

export type RenderedSavedPrompt =
  | { text: string; problem?: never; field?: never }
  | { problem: ParseProblem | "value_too_long" | "output_too_long"; field?: string; text?: never };

export function renderSavedPromptTemplate(template: SavedPromptTemplate, values: ReadonlyMap<string, string>): RenderedSavedPrompt {
  if (template.problem) return { problem: template.problem };
  const lengths = new Map<string, number>();
  for (const field of template.fields) {
    const length = countPoints(values.get(field) ?? "");
    if (length > VALUE_MAX) return { problem: "value_too_long", field };
    lengths.set(field, length);
  }
  let size = 0;
  const output: string[] = [];
  for (const part of template.parts) {
    if (typeof part === "string") {
      size += countPoints(part);
      output.push(part);
    } else {
      size += lengths.get(part.field) ?? 0;
      output.push(values.get(part.field) ?? "");
    }
    if (size > OUTPUT_MAX) return { problem: "output_too_long" };
  }
  return { text: output.join("") };
}
