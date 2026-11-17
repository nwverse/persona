// The portable settings form contract (#9877; the hub's server/internal/configuration/types.go, docs/web/web.md
// "Configuration forms"): a module describes one resource's settings as a Document (titled groups of typed fields with
// their effective values) and takes an Update that names only what changes. The same JSON is read by every client.
//
// This file knows no module, user, assistant, hub or storage: a ConfigurationSource injected by the owning module reads
// and updates its one resource. Validation here mirrors the writer's so a form can say what is wrong before it asks;
// the writer still checks everything itself.

export const CONFIGURATION_VERSION = 1;

export interface ConfigurationDetail {
  title: string;
  description: string;
}

export interface ConfigurationNotice {
  kind: "info" | "warning" | string;
  message: string;
}

export interface ConfigurationOption {
  value: string;
  title: string;
  description?: string;
}

export interface TextOptions {
  multiline?: boolean;
  placeholder?: string;
  /** Unicode scalar values, not UTF-16 units. */
  min_length?: number;
  max_length?: number;
}

export interface SelectOptions {
  options: ConfigurationOption[];
  presentation?: "list" | "dropdown" | "segmented" | string;
}

export interface MultiselectOptions {
  options: ConfigurationOption[];
  min_selected?: number;
  max_selected?: number;
}

export interface NumberOptions {
  integer?: boolean;
  minimum?: number;
  maximum?: number;
  /** A positive increment for the UI, not a constraint. */
  step?: number;
}

interface FieldBase {
  id: string;
  title: string;
  description?: string;
  read_only?: boolean;
  disabled_reason?: string;
  details?: ConfigurationDetail[];
}

export type ToggleField = FieldBase & { kind: "toggle"; default: boolean };
export type TextField = FieldBase & { kind: "text"; default: string; text?: TextOptions };
export type SelectField = FieldBase & { kind: "select"; default: string; select: SelectOptions };
export type MultiselectField = FieldBase & { kind: "multiselect"; default: string[]; multiselect: MultiselectOptions };
export type NumberField = FieldBase & { kind: "number"; default: number; number?: NumberOptions };
export type KnownField = ToggleField | TextField | SelectField | MultiselectField | NumberField;
/** A field this client cannot draw or check (a newer kind, or a definition that isn't valid): shown, never changed. */
export type UnsupportedField = FieldBase & { kind: "unsupported"; reason: "kind" | "definition" };
export type ConfigurationField = KnownField | UnsupportedField;

export type FieldValue = boolean | string | string[] | number;

export interface ConfigurationGroup {
  id: string;
  title: string;
  description?: string;
  fields: ConfigurationField[];
}

/** A complete snapshot of one resource. `values` holds one effective value per field, defaults included. */
export interface ConfigurationDocument {
  schema_version: number;
  id: string;
  /** Opaque; changes whenever values, permissions or definitions change. */
  revision: string;
  title: string;
  description?: string;
  groups: ConfigurationGroup[];
  values: Record<string, unknown>;
  notices?: Record<string, ConfigurationNotice>;
}

/** Changes only the fields it names; `reset` removes their overrides. Atomic, against `revision`. */
export interface ConfigurationUpdate {
  schema_version: number;
  id: string;
  revision: string;
  changes?: Record<string, FieldValue>;
  reset?: string[];
}

export interface ConfigurationFieldError {
  id: string;
  code: string;
  message?: string;
}

/** What a source rejects with: the API's `{ error, code, field_errors }`. A conflict carries no replacement values. */
export class ConfigurationError extends Error {
  constructor(
    readonly code: string,
    message = "",
    readonly fieldErrors: ConfigurationFieldError[] = [],
  ) {
    super(message);
    this.name = "ConfigurationError";
  }

  /** From an `{ error, code, field_errors }` body; anything else is a generic failure with no message to show. */
  static fromBody(body: unknown, fallback = "failed"): ConfigurationError {
    const b = isObject(body) ? body : {};
    const code = typeof b.code === "string" && b.code ? b.code : fallback;
    const message = typeof b.error === "string" ? b.error : "";
    const list = Array.isArray(b.field_errors) ? b.field_errors : [];
    const fields = list.flatMap((f): ConfigurationFieldError[] =>
      isObject(f) && typeof f.id === "string" && typeof f.code === "string" ? [{ id: f.id, code: f.code, ...(typeof f.message === "string" ? { message: f.message } : {}) }] : [],
    );
    return new ConfigurationError(code, message, fields);
  }
}

/** Codes this client says in its own words; any other code shows the writer's message (or a generic line). */
export const ERROR_CODES = ["conflict", "unsupported_version", "read_only", "invalid_update", "invalid_document", "not_found", "forbidden", "invalid_type", "too_short", "too_long", "invalid_choice", "duplicate", "too_few_selected", "too_many_selected", "not_integer", "out_of_range"] as const;

/**
 * One resource's settings, as its owning module provides them. `key` names the bound resource: a different key is a
 * different resource (the form starts over), the same key the same one. `subscribe`, when the module can tell, calls back
 * when the resource may have changed elsewhere; the form reads it again.
 */
export interface ConfigurationSource {
  readonly key: string;
  read(signal?: AbortSignal): Promise<unknown>;
  update(update: ConfigurationUpdate, signal?: AbortSignal): Promise<unknown>;
  subscribe?(onChange: () => void): () => void;
}

// ── checking ─────────────────────────────────────────────

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

const str = (v: unknown): v is string => typeof v === "string";
const optStr = (v: unknown) => v === undefined || str(v);
/** A count bound (a length, a number of choices): a non-negative safe integer, or absent. */
const optCount = (v: unknown) => v === undefined || (Number.isSafeInteger(v) && (v as number) >= 0);
/** A finite number inside the safe integer range, decimals allowed. */
export const safeNumber = (v: unknown): v is number => typeof v === "number" && Number.isFinite(v) && Math.abs(v) <= Number.MAX_SAFE_INTEGER;
const optSafe = (v: unknown) => v === undefined || safeNumber(v);

/** Length in Unicode scalar values (what the writer counts), not UTF-16 units. */
export const scalarLength = (s: string) => [...s].length;

function details(v: unknown): ConfigurationDetail[] | undefined | null {
  if (v === undefined) return undefined;
  if (!Array.isArray(v)) return null;
  return v.every((d) => isObject(d) && str(d.title) && str(d.description)) ? (v as ConfigurationDetail[]) : null;
}

function options(v: unknown): ConfigurationOption[] | null {
  if (!Array.isArray(v) || !v.length) return null;
  if (!v.every((o) => isObject(o) && str(o.value) && str(o.title) && optStr(o.description))) return null;
  const values = v.map((o) => (o as ConfigurationOption).value);
  return new Set(values).size === values.length ? (v as ConfigurationOption[]) : null;
}

/** A field as this client can use it: a known kind with a valid definition and default, or unsupported (kept, shown). */
export function parseField(raw: unknown): ConfigurationField | null {
  if (!isObject(raw) || !str(raw.id) || !raw.id || !str(raw.kind) || !str(raw.title)) return null;
  const d = details(raw.details);
  const base: FieldBase = {
    id: raw.id,
    title: raw.title,
    ...(str(raw.description) ? { description: raw.description } : {}),
    ...(raw.read_only === true ? { read_only: true } : {}),
    ...(str(raw.disabled_reason) && raw.disabled_reason ? { disabled_reason: raw.disabled_reason } : {}),
    ...(d ? { details: d } : {}),
  };
  const unsupported = (reason: "kind" | "definition"): UnsupportedField => ({ ...base, kind: "unsupported", reason });
  if (d === null || !optStr(raw.description) || !optStr(raw.disabled_reason)) return unsupported("definition");
  let field: KnownField;
  switch (raw.kind) {
    case "toggle":
      field = { ...base, kind: "toggle", default: raw.default as boolean };
      break;
    case "text": {
      const o = raw.text === undefined ? {} : raw.text;
      if (!isObject(o) || !optStr(o.placeholder) || !optCount(o.min_length) || !optCount(o.max_length)) return unsupported("definition");
      if (o.min_length !== undefined && o.max_length !== undefined && (o.min_length as number) > (o.max_length as number)) return unsupported("definition");
      field = { ...base, kind: "text", default: raw.default as string, text: { ...(o.multiline === true ? { multiline: true } : {}), ...(o.placeholder ? { placeholder: o.placeholder as string } : {}), ...(o.min_length !== undefined ? { min_length: o.min_length as number } : {}), ...(o.max_length !== undefined ? { max_length: o.max_length as number } : {}) } };
      break;
    }
    case "select": {
      const o = raw.select;
      const list = isObject(o) ? options(o.options) : null;
      if (!isObject(o) || !list || !optStr(o.presentation)) return unsupported("definition");
      field = { ...base, kind: "select", default: raw.default as string, select: { options: list, ...(o.presentation ? { presentation: o.presentation as string } : {}) } };
      break;
    }
    case "multiselect": {
      const o = raw.multiselect;
      const list = isObject(o) ? options(o.options) : null;
      if (!isObject(o) || !list || !optCount(o.min_selected) || !optCount(o.max_selected)) return unsupported("definition");
      if (o.min_selected !== undefined && o.max_selected !== undefined && (o.min_selected as number) > (o.max_selected as number)) return unsupported("definition");
      field = { ...base, kind: "multiselect", default: raw.default as string[], multiselect: { options: list, ...(o.min_selected !== undefined ? { min_selected: o.min_selected as number } : {}), ...(o.max_selected !== undefined ? { max_selected: o.max_selected as number } : {}) } };
      break;
    }
    case "number": {
      const o = raw.number === undefined ? {} : raw.number;
      if (!isObject(o) || !optSafe(o.minimum) || !optSafe(o.maximum) || !optSafe(o.step)) return unsupported("definition");
      if (o.step !== undefined && !((o.step as number) > 0)) return unsupported("definition");
      if (o.minimum !== undefined && o.maximum !== undefined && (o.minimum as number) > (o.maximum as number)) return unsupported("definition");
      field = { ...base, kind: "number", default: raw.default as number, number: { ...(o.integer === true ? { integer: true } : {}), ...(o.minimum !== undefined ? { minimum: o.minimum as number } : {}), ...(o.maximum !== undefined ? { maximum: o.maximum as number } : {}), ...(o.step !== undefined ? { step: o.step as number } : {}) } };
      break;
    }
    default:
      return unsupported("kind");
  }
  return checkValue(field, field.default) ? unsupported("definition") : field;
}

/** Why `value` is not a value of `field` (an error code), or null when it is one. */
export function checkValue(field: KnownField, value: unknown): string | null {
  switch (field.kind) {
    case "toggle":
      return typeof value === "boolean" ? null : "invalid_type";
    case "text": {
      if (!str(value)) return "invalid_type";
      const n = scalarLength(value);
      if (field.text?.min_length !== undefined && n < field.text.min_length) return "too_short";
      if (field.text?.max_length !== undefined && n > field.text.max_length) return "too_long";
      return null;
    }
    case "select":
      return !str(value) ? "invalid_type" : field.select.options.some((o) => o.value === value) ? null : "invalid_choice";
    case "multiselect": {
      if (!Array.isArray(value) || !value.every(str)) return "invalid_type";
      if (new Set(value).size !== value.length) return "duplicate";
      if (!value.every((v) => field.multiselect.options.some((o) => o.value === v))) return "invalid_choice";
      if (field.multiselect.min_selected !== undefined && value.length < field.multiselect.min_selected) return "too_few_selected";
      if (field.multiselect.max_selected !== undefined && value.length > field.multiselect.max_selected) return "too_many_selected";
      return null;
    }
    case "number": {
      if (!safeNumber(value)) return typeof value === "number" ? "out_of_range" : "invalid_type";
      if (field.number?.integer && !Number.isInteger(value)) return "not_integer";
      if (field.number?.minimum !== undefined && value < field.number.minimum) return "out_of_range";
      if (field.number?.maximum !== undefined && value > field.number.maximum) return "out_of_range";
      return null;
    }
  }
}

/** A Document as this client reads it. `writable` is false for a schema version it doesn't know (shown, never
 *  written). Throws ConfigurationError("invalid_document") for something that isn't a Document at all. */
export interface ParsedDocument {
  doc: ConfigurationDocument;
  writable: boolean;
}

export function parseDocument(raw: unknown): ParsedDocument {
  const bad = () => new ConfigurationError("invalid_document");
  if (!isObject(raw) || !Number.isSafeInteger(raw.schema_version) || !str(raw.id) || !str(raw.revision) || !str(raw.title)) throw bad();
  const writable = raw.schema_version === CONFIGURATION_VERSION;
  const values = isObject(raw.values) ? raw.values : null;
  const groupsRaw = Array.isArray(raw.groups) ? raw.groups : null;
  if (writable && (!values || !groupsRaw)) throw bad();
  const groups: ConfigurationGroup[] = [];
  const ids = new Set<string>();
  for (const g of groupsRaw ?? []) {
    if (!isObject(g) || !str(g.id) || !str(g.title) || !Array.isArray(g.fields)) {
      if (writable) throw bad();
      continue;
    }
    const fields: ConfigurationField[] = [];
    for (const f of g.fields) {
      const field = parseField(f);
      if (!field || ids.has(field.id)) {
        if (writable) throw bad();
        continue;
      }
      ids.add(field.id);
      fields.push(field);
    }
    groups.push({ id: g.id, title: g.title, ...(str(g.description) && g.description ? { description: g.description } : {}), fields });
  }
  const notices: Record<string, ConfigurationNotice> = {};
  if (isObject(raw.notices)) {
    for (const [id, n] of Object.entries(raw.notices)) if (isObject(n) && str(n.kind) && str(n.message)) notices[id] = { kind: n.kind, message: n.message };
  }
  return {
    doc: { schema_version: raw.schema_version as number, id: raw.id, revision: raw.revision, title: raw.title, ...(str(raw.description) && raw.description ? { description: raw.description } : {}), groups, values: values ?? {}, notices },
    writable,
  };
}

/** The field's current value when it is a valid one; undefined when the document holds none or an invalid one. */
export function fieldValue(doc: ConfigurationDocument, field: KnownField): FieldValue | undefined {
  const v = doc.values[field.id];
  return checkValue(field, v) ? undefined : (v as FieldValue);
}

/** Whether two values of a field are the same (a multiselect's choices in any order). */
export function sameValue(a: unknown, b: unknown): boolean {
  if (Array.isArray(a) && Array.isArray(b)) return a.length === b.length && a.every((v) => b.includes(v));
  return Object.is(a, b);
}
