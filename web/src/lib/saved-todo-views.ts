import { useEffect, useMemo, useSyncExternalStore } from "react";
import { t } from "../i18n";
import { api, ApiError, credentialOwner, errorText, getToken, subscribeCredentialOwner } from "./api";
import { chosenManager, defaultKnown, messageManager, onDefaultManager, onManagerChoice, shownManager, stampedHeaders } from "./manager-choice";
import { onHubEvent } from "./store";
import { isTodoListFilter, isTodoListSort, type TodoListFilter, type TodoListSort } from "./todo-list-filter";

export const TODO_VIEW_LIMIT = 50;
export const TODO_VIEW_NAME_LIMIT = 64;
const TEXT_LIMIT = 1024;
const PATH = "/assistant/saved-todo-views";

export interface SavedTodoCriteria {
  text: string;
  due_window: TodoListFilter;
  section_sort: "default" | Exclude<TodoListSort, "recent">;
}

export interface SavedTodoView {
  id: string;
  name: string;
  criteria: SavedTodoCriteria;
  created_at: string;
  updated_at: string;
}

export interface SavedTodoViewsDocument {
  schema_version: 1;
  revision: string;
  views: SavedTodoView[];
  can_write: boolean;
}

export type TodoViewMutation =
  | { op: "add"; name: string; criteria: SavedTodoCriteria }
  | { op: "update"; id: string; name: string; criteria: SavedTodoCriteria }
  | { op: "delete"; id: string };

export const todoViewCriteria = (text: string, filter: TodoListFilter, sort: TodoListSort): SavedTodoCriteria => ({
  text: text.trim(), due_window: filter, section_sort: sort === "recent" ? "default" : sort,
});
export const todoViewSort = (criteria: SavedTodoCriteria): TodoListSort => criteria.section_sort === "default" ? "recent" : criteria.section_sort;

const isLabel = (name: string) => !!name && [...name].length <= TODO_VIEW_NAME_LIMIT && !/\p{Cc}/u.test(name);
const isLiteral = (text: string) => new TextEncoder().encode(text).length <= TEXT_LIMIT && !/\p{Cc}/u.test(text);
const validRevision = (value: unknown): value is string => typeof value === "string" &&
  /^(?:0|[1-9][0-9]{0,18})$/.test(value) && BigInt(value) <= 9223372036854775807n;

export function todoViewProblem(name: string, criteria: SavedTodoCriteria): string {
  if (!isLabel(name.trim())) return t("saved-todo-views.name_invalid");
  if (!isLiteral(criteria.text.trim())) return t("saved-todo-views.text_invalid");
  return "";
}

function readDocument(document: SavedTodoViewsDocument): SavedTodoViewsDocument {
  if (!document || document.schema_version !== 1 || !validRevision(document.revision) || typeof document.can_write !== "boolean" ||
      !Array.isArray(document.views) || document.views.length > TODO_VIEW_LIMIT || document.views.some((view) =>
        !view || typeof view.id !== "string" || !/^view_[0-9a-f]{24}$/.test(view.id) ||
        typeof view.name !== "string" || !isLabel(view.name) || !view.criteria ||
        typeof view.criteria.text !== "string" || !isLiteral(view.criteria.text) || !isTodoListFilter(view.criteria.due_window) ||
        (view.criteria.section_sort !== "default" && (!isTodoListSort(view.criteria.section_sort) || (view.criteria.section_sort as string) === "recent"))
      ) || new Set(document.views.map((view) => view.id)).size !== document.views.length) {
    throw new Error(t("saved-todo-views.invalid_response"));
  }
  return document;
}

let generation = 0;
const currentScope = () => generation;
const routeKnown = () => defaultKnown() || chosenManager() !== null;
function subscribeScope(listener: () => void) {
  let manager = shownManager();
  let known = routeKnown();
  const changed = () => { generation++; listener(); };
  const routingChanged = () => {
    const nextManager = shownManager();
    const nextKnown = routeKnown();
    if (nextManager === manager && nextKnown === known) return;
    manager = nextManager;
    known = nextKnown;
    changed();
  };
  const stop = [onManagerChoice(routingChanged), onDefaultManager(routingChanged), subscribeCredentialOwner(changed)];
  return () => stop.forEach((off) => off());
}

type State = { document: SavedTodoViewsDocument | null; loading: boolean; busy: boolean; error: string; unsupported: boolean; conflict: boolean };
const initial: State = { document: null, loading: true, busy: false, error: "", unsupported: false, conflict: false };

export class SavedTodoViewsModel {
  private token = getToken();
  private listeners = new Set<() => void>();
  private state: State = initial;
  private active?: AbortController;
  private serial = 0;
  private refreshAfterWrite = false;

  constructor(readonly manager: string, private owner: string, readonly scope: number) {}

  snapshot = () => this.state;
  subscribe = (listener: () => void) => (this.listeners.add(listener), () => { this.listeners.delete(listener); });
  private set(patch: Partial<State>) {
    this.state = { ...this.state, ...patch };
    this.listeners.forEach((listener) => listener());
  }

  private current = () => !!this.active && !this.active.signal.aborted && this.scope === currentScope() &&
    this.manager === shownManager() && this.owner === credentialOwner() && routeKnown();

  private options(controller: AbortController) {
    return {
      token: this.token, tokenOwner: this.owner, headers: stampedHeaders(this.manager), signal: controller.signal,
      isAuthContextCurrent: () => this.active === controller && this.current(),
    };
  }

  follow() {
    const controller = new AbortController();
    this.active = controller;
    void this.refresh();
    const replay = (event?: Record<string, unknown>) => {
      if (event && messageManager(event) !== this.manager) return;
      if (this.state.conflict) return; // a draft keeps its reviewed version until the owner asks to reload
      if (this.state.busy) this.refreshAfterWrite = true;
      else void this.refresh();
    };
    const saved = onHubEvent("saved_todo_views", replay);
    const hello = onHubEvent("hello", () => replay());
    return () => {
      saved();
      hello();
      controller.abort();
      this.active = undefined;
      this.serial++;
      this.refreshAfterWrite = false;
      this.state = initial;
    };
  }

  async refresh(): Promise<SavedTodoViewsDocument | undefined> {
    const controller = this.active;
    if (!controller || !this.current() || this.state.busy) return;
    const request = ++this.serial;
    const options = this.options(controller);
    this.set({ loading: true });
    try {
      const document = readDocument(await api<SavedTodoViewsDocument>(PATH, options));
      if (!options.isAuthContextCurrent() || request !== this.serial) return;
      if (this.state.document && BigInt(document.revision) < BigInt(this.state.document.revision)) return this.state.document;
      this.set({ document, unsupported: false, conflict: false, error: "" });
      return document;
    } catch (failure) {
      if (!options.isAuthContextCurrent() || request !== this.serial) return;
      const unsupported = failure instanceof ApiError &&
        (failure.status === 404 && failure.code !== "no_manager" || failure.status === 503 && failure.code === "not_supported");
      this.set(unsupported ? { document: null, unsupported: true, error: "" } : { error: errorText(failure) });
    } finally {
      if (options.isAuthContextCurrent() && request === this.serial) this.set({ loading: false });
    }
  }

  async write(mutation: TodoViewMutation, revision: string): Promise<boolean> {
    const controller = this.active;
    if (!controller || !this.current() || this.state.busy || !this.state.document?.can_write || this.state.conflict) return false;
    if (revision !== this.state.document.revision) {
      this.set({ conflict: true, error: t("saved-todo-views.conflict") });
      return false;
    }
    const options = this.options(controller);
    this.serial++;
    this.set({ busy: true, loading: false, error: "" });
    try {
      const document = readDocument(await api<SavedTodoViewsDocument>(PATH, {
        ...options, method: "POST", json: { revision, ...mutation },
      }));
      if (!options.isAuthContextCurrent()) return false;
      this.set({ document, conflict: false });
      return true;
    } catch (failure) {
      if (!options.isAuthContextCurrent()) return false;
      const conflict = failure instanceof ApiError && failure.status === 409 && (failure.code === "conflict" || failure.code === undefined);
      this.set({ conflict, error: conflict ? t("saved-todo-views.conflict") : errorText(failure) });
      return false;
    } finally {
      if (options.isAuthContextCurrent()) {
        this.set({ busy: false });
        if (this.refreshAfterWrite && !this.state.conflict) {
          this.refreshAfterWrite = false;
          void this.refresh();
        }
      }
    }
  }
}

export function useSavedTodoViews(enabled: boolean, manager: string, owner: string) {
  const scope = useSyncExternalStore(subscribeScope, currentScope, currentScope);
  const model = useMemo(() => new SavedTodoViewsModel(manager, owner, scope), [manager, owner, scope]);
  useEffect(() => enabled ? model.follow() : undefined, [enabled, model]);
  const state = useSyncExternalStore(model.subscribe, model.snapshot, model.snapshot);
  return { model, scope, state };
}
