// The state of one configuration form (contract.ts) over one injected source: reading, the text a person is typing,
// one write at a time against the revision they saw, and what came back. Framework-free (ConfigurationForm.tsx draws it)
// so its timing can be tested on its own.
//
// - Nothing shows as saved before the source has committed and returned the new Document; a choice being written shows
//   as pending, and a failure puts back what the Document says.
// - Typed text (and a typed number) is a draft until Save: it survives a failed write, a conflict, a re-read and a change
//   made elsewhere, and goes only when saved or discarded.
// - A conflict replaces nothing: the form says so and reads again only when asked, keeping every draft.
// - A controller belongs to one source: a different source is a new controller, and a disposed one ignores every late
//   answer. A read that started before a newer Document arrived is ignored too.
import {
  checkValue,
  CONFIGURATION_VERSION,
  ConfigurationError,
  parseDocument,
  safeNumber,
  type ConfigurationDocument,
  type ConfigurationSource,
  type FieldValue,
  type KnownField,
} from "./contract";

export interface Problem {
  code: string;
  message?: string;
}

export interface FieldState {
  /** Typed and not saved yet (text and number fields), as typed. */
  draft?: string;
  /** A value being written; shown as pending until the source answers. */
  pending?: FieldValue;
  error?: Problem;
  /** The last write of this field committed. */
  saved?: boolean;
}

export interface FormState {
  phase: "loading" | "ready" | "failed";
  doc: ConfigurationDocument | null;
  /** False for a schema version this client doesn't know: shown, never written. */
  writable: boolean;
  /** Why the Document couldn't be read (phase "failed"), or couldn't be read again (kept, phase "ready"). */
  readError: Problem | null;
  /** The field being written (one write at a time: each is against the current revision). */
  busy: string | null;
  /** The source said the resource changed since this revision: nothing was written. */
  conflict: boolean;
  /** A write's failure that names no field of this Document. */
  formError: Problem | null;
  fields: Record<string, FieldState>;
}

const INITIAL: FormState = { phase: "loading", doc: null, writable: false, readError: null, busy: null, conflict: false, formError: null, fields: {} };

function problem(e: unknown): Problem {
  if (e instanceof ConfigurationError) return e.message ? { code: e.code, message: e.message } : { code: e.code };
  return { code: "failed" };
}

const aborted = (e: unknown) => e instanceof DOMException && e.name === "AbortError";

/** A typed number as a value: "" or anything that isn't a finite number in the safe range is not one. */
export function parseNumber(raw: string): number | null {
  const s = raw.trim();
  if (!s) return null;
  const n = Number(s);
  return safeNumber(n) ? n : null;
}

/** What a typed draft means for `field`: its value, or the code of what is wrong with it. */
export function draftValue(field: KnownField, raw: string): { value: FieldValue } | { code: string } {
  if (field.kind === "number") {
    const n = parseNumber(raw);
    if (n === null) return { code: raw.trim() ? "out_of_range" : "invalid_type" };
    const code = checkValue(field, n);
    return code ? { code } : { value: n };
  }
  const code = checkValue(field, raw);
  return code ? { code } : { value: raw };
}

export class ConfigurationController {
  private state: FormState = INITIAL;
  private listeners = new Set<() => void>();
  private disposed = false;
  private abort = new AbortController();
  private unsubscribe: (() => void) | null = null;
  /** Counts Documents applied: a read that started before the last one is stale. */
  private applied = 0;
  private readSeq = 0;
  /** The resource changed while a write was in flight: read again once it is done. */
  private again = false;

  constructor(readonly source: ConfigurationSource) {}

  subscribe = (l: () => void) => {
    this.listeners.add(l);
    return () => {
      this.listeners.delete(l);
    };
  };

  snapshot = () => this.state;

  private set(next: Partial<FormState>) {
    if (this.disposed) return;
    this.state = { ...this.state, ...next };
    this.listeners.forEach((l) => l());
  }

  private field(id: string, next: FieldState | null) {
    const fields = { ...this.state.fields };
    const kept = next ? (Object.fromEntries(Object.entries(next).filter(([, v]) => v !== undefined)) as FieldState) : null;
    if (kept && Object.keys(kept).length) fields[id] = kept;
    else delete fields[id];
    return fields;
  }

  start() {
    void this.read(true);
    this.unsubscribe = this.source.subscribe?.(() => {
      if (this.state.busy) this.again = true;
      else void this.read(false);
    }) ?? null;
  }

  dispose() {
    this.disposed = true;
    this.abort.abort();
    this.unsubscribe?.();
    this.listeners.clear();
  }

  /** Reads the Document again (Retry, Reload after a conflict); every draft stays. */
  reload() {
    void this.read(true);
  }

  private async read(explicit: boolean) {
    const seq = ++this.readSeq;
    const applied = this.applied;
    if (explicit && this.state.phase === "failed") this.set({ phase: "loading", readError: null });
    try {
      const parsed = parseDocument(await this.source.read(this.abort.signal));
      if (this.disposed || seq !== this.readSeq || applied !== this.applied) return;
      // the same revision is the same Document (the contract changes it with any change): leave pending marks and
      // "Saved" alone, as when a source tells of its own write
      const same = this.state.doc && parsed.doc.id === this.state.doc.id && parsed.doc.revision === this.state.doc.revision && parsed.writable === this.state.writable;
      if (!same) this.apply(parsed.doc, parsed.writable);
      this.set({ phase: "ready", readError: null, ...(explicit ? { conflict: false, formError: null } : {}) });
    } catch (e) {
      if (this.disposed || aborted(e) || seq !== this.readSeq) return;
      this.set(this.state.doc ? { readError: problem(e) } : { phase: "failed", readError: problem(e) });
    }
  }

  private apply(doc: ConfigurationDocument, writable: boolean) {
    this.applied++;
    // drafts stay (they are what the person typed); pending choices and saved marks belong to the old Document
    const fields: Record<string, FieldState> = {};
    for (const [id, f] of Object.entries(this.state.fields)) if (f.draft !== undefined) fields[id] = { draft: f.draft, ...(f.error ? { error: f.error } : {}) };
    this.state = { ...this.state, doc, writable, fields };
  }

  private known(id: string): KnownField | null {
    for (const g of this.state.doc?.groups ?? []) for (const f of g.fields) if (f.id === id) return f.kind === "unsupported" ? null : f;
    return null;
  }

  /** Whether `id` can be written now (a known, writable field of a writable Document, nothing else in flight). */
  canWrite(id: string): boolean {
    const f = this.known(id);
    return !!f && this.state.writable && !this.state.busy && !f.read_only && !f.disabled_reason;
  }

  setDraft(id: string, raw: string) {
    const f = this.state.fields[id] ?? {};
    this.set({ fields: this.field(id, { ...f, draft: raw, error: undefined, saved: undefined }) });
  }

  discard(id: string) {
    const f = this.state.fields[id];
    if (!f) return;
    this.set({ fields: this.field(id, { ...f, draft: undefined, error: undefined }) });
  }

  /** Saves a typed draft, after checking it here first. */
  saveDraft(id: string) {
    const field = this.known(id);
    const raw = this.state.fields[id]?.draft;
    if (!field || raw === undefined || !this.canWrite(id)) return;
    const r = draftValue(field, raw);
    if ("code" in r) {
      this.set({ fields: this.field(id, { ...this.state.fields[id], error: { code: r.code } }) });
      return;
    }
    void this.write(id, { changes: { [id]: r.value } });
  }

  /** Writes a choice (a switch, a selection, a set of checks) at once; it shows as pending until committed. */
  choose(id: string, value: FieldValue) {
    const field = this.known(id);
    if (!field || !this.canWrite(id)) return;
    const code = checkValue(field, value);
    if (code) {
      this.set({ fields: this.field(id, { ...this.state.fields[id], error: { code } }) });
      return;
    }
    void this.write(id, { changes: { [id]: value } }, value);
  }

  /** Back to the default: the override is removed (the source decides what the default is). */
  reset(id: string) {
    if (!this.canWrite(id)) return;
    void this.write(id, { reset: [id] });
  }

  private async write(id: string, change: { changes?: Record<string, FieldValue>; reset?: string[] }, pending?: FieldValue) {
    const doc = this.state.doc!;
    const before = this.state.fields[id] ?? {};
    this.set({ busy: id, formError: null, fields: this.field(id, { ...before, error: undefined, saved: undefined, ...(pending !== undefined ? { pending } : {}) }) });
    try {
      const raw = await this.source.update({ schema_version: CONFIGURATION_VERSION, id: doc.id, revision: doc.revision, ...change }, this.abort.signal);
      if (this.disposed) return;
      const parsed = parseDocument(raw);
      if (parsed.doc.id !== doc.id) throw new ConfigurationError("invalid_document");
      this.apply(parsed.doc, parsed.writable);
      // the field is what was committed now: its draft went with it (a reset replaces it with the default)
      this.set({ busy: null, conflict: false, fields: this.field(id, { saved: true }) });
    } catch (e) {
      if (this.disposed || aborted(e)) return;
      const p = problem(e);
      const keep: FieldState = { ...(before.draft !== undefined ? { draft: before.draft } : {}) };
      if (p.code === "conflict") {
        this.set({ busy: null, conflict: true, fields: this.field(id, keep) });
      } else {
        let fields = this.field(id, keep);
        let named = false;
        if (e instanceof ConfigurationError) {
          for (const fe of e.fieldErrors) {
            if (!this.known(fe.id)) continue;
            named = true;
            fields = { ...fields, [fe.id]: { ...fields[fe.id], error: fe.message ? { code: fe.code, message: fe.message } : { code: fe.code } } };
          }
        }
        if (!named) fields = { ...fields, [id]: { ...fields[id], error: p } };
        this.set({ busy: null, fields });
      }
    } finally {
      if (this.again && !this.disposed) {
        this.again = false;
        void this.read(false);
      }
    }
  }
}
