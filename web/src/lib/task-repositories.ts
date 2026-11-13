import type { Todo } from "./todos";
import { api, credentialOwner, getToken, pastHub } from "./api";
import { MANAGER_HEADER } from "./manager-choice";
import type { RepositoryIdentity } from "./repository-identity";

export type RepositoryBinding = RepositoryIdentity & {
  id: string; name?: string; aliases?: string[]; purpose?: string; verified_at: string; clone_url?: string;
};
export type RequestedRef = { kind: "default"; value?: never } | { kind: "branch" | "tag" | "commit" | "ref"; value: string };
export type RepositoryLayout = "root" | "container";
export type RepositorySourceSnapshot = {
  identity: RepositoryIdentity; clone_url: string; transport_origin: "catalog" | "canonical_default"; catalog_revision: number;
};
export type RepositoryLinkInput = { url: string; kind?: never; number?: never } | { kind: "issue" | "pull_request"; number: string; url?: never };
export type TaskRepositoryLink = { kind: "issue" | "pull_request"; number: string; canonical_url: string };
export type TaskRepositoryInput = { repo_id: string; directory_alias?: string; requested_ref?: RequestedRef; links?: RepositoryLinkInput[] };
export type TaskRepository = Omit<TaskRepositoryInput, "links" | "requested_ref"> & {
  requested_ref: RequestedRef; source_snapshot: RepositorySourceSnapshot; links: TaskRepositoryLink[];
  catalog_state?: "current" | "removed" | "identity_changed";
};
export type TaskRepositorySet = { schema_version: 1; manager: string; todo_id: string; revision: number; repositories: TaskRepository[] };
export type RepositoryCatalog = { manager: string; catalog_revision: number; repositories: RepositoryBinding[] };
export type CreateRepositoryBindings = { catalog_revision: number; repositories: TaskRepositoryInput[] };
export type TaskRepositoryPreparationSnapshot = {
  schema_version: 1; manager: string; todo_id: string; revision: number; digest: string;
  repositories: Array<Pick<TaskRepository, "repo_id" | "directory_alias" | "requested_ref" | "source_snapshot">>;
};

/** The same conservative directory-name rule as repository.ValidateAlias in Go. Empty is no override. */
export function validRepositoryAlias(alias: string): boolean {
  if (!alias) return true;
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(alias) || alias.endsWith(".")) return false;
  return !/^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\.|$)/i.test(alias);
}
export function repositoryRelativePath(layout: RepositoryLayout, alias: string, count: number): string | undefined {
  if (layout === "root" && count === 1) return ".";
  return layout === "container" && !!alias && validRepositoryAlias(alias) ? alias : undefined;
}
export function repositoryDraft(rows: TaskRepository[]): TaskRepositoryInput[] {
  return rows.map((row) => ({ repo_id: row.repo_id, directory_alias: row.directory_alias, requested_ref: { ...row.requested_ref },
    links: row.links.map(({ kind, number }) => ({ kind, number })) }));
}
const revision = (n: unknown): n is number => Number.isSafeInteger(n) && (n as number) >= 0;
// Check the server-owned envelope; repository normalization remains in the shared parser.
function validIdentity(identity: RepositoryIdentity): boolean {
  if (!identity || !["github", "gitlab"].includes(identity.provider) || typeof identity.instance_url !== "string"
    || typeof identity.path !== "string" || !identity.path || identity.canonical_url !== `${identity.instance_url}/${identity.path}`) return false;
  try {
    const instance = new URL(identity.instance_url);
    return ["https:", "http:"].includes(instance.protocol) && instance.origin === identity.instance_url;
  } catch { return false; }
}
export function validRequestedRef(ref: RequestedRef): boolean {
  if (!ref || typeof ref.kind !== "string") return false;
  if (ref.kind === "default") return ref.value === undefined;
  return ["branch", "tag", "commit", "ref"].includes(ref.kind) && typeof ref.value === "string" && !!ref.value;
}
export function validRepositorySet(set: TaskRepositorySet, manager: string, todoId: string): boolean {
  return !!set && set.schema_version === 1 && set.manager === manager && set.todo_id === todoId && revision(set.revision)
    && Array.isArray(set.repositories) && set.repositories.length <= 20 && set.repositories.every((row) =>
      row && typeof row.repo_id === "string" && (row.directory_alias === undefined || typeof row.directory_alias === "string" && validRepositoryAlias(row.directory_alias))
      && validRequestedRef(row.requested_ref) && row.source_snapshot && validIdentity(row.source_snapshot.identity)
      && typeof row.source_snapshot.clone_url === "string" && revision(row.source_snapshot.catalog_revision)
      && ["catalog", "canonical_default"].includes(row.source_snapshot.transport_origin)
      && Array.isArray(row.links) && row.links.every((link) => ["issue", "pull_request"].includes(link.kind)
        && typeof link.number === "string" && /^[1-9][0-9]{0,19}$/.test(link.number) && typeof link.canonical_url === "string"));
}
export type RepositoryRequestContext = { manager: string; owner: string; token: string; isCurrent: () => boolean };
export function repositoryRequestContext(manager: string, owner: string, active: () => boolean): RepositoryRequestContext {
  return { manager, owner, token: getToken(), isCurrent: () => active() && credentialOwner() === owner };
}
export function repositoryRequestOptions(context: RepositoryRequestContext) {
  if (!context.isCurrent()) throw new Error("The editor connection changed");
  return { token: context.token, tokenOwner: context.owner, isAuthContextCurrent: context.isCurrent, headers: { [MANAGER_HEADER]: context.manager } };
}
export async function loadRepositoryCatalog(context: RepositoryRequestContext): Promise<RepositoryCatalog> {
  const result = await api<RepositoryCatalog>("/assistant/repositories", repositoryRequestOptions(context));
  if (!context.isCurrent() || result.manager !== context.manager || !revision(result.catalog_revision)
    || !Array.isArray(result.repositories) || !result.repositories.every((r) => typeof r.id === "string" && validIdentity(r))) throw new Error("Invalid repository catalog response");
  return result;
}
export async function loadTaskRepositories(context: RepositoryRequestContext, todoId: string): Promise<TaskRepositorySet> {
  const result = await api<TaskRepositorySet>(`/todos/${encodeURIComponent(todoId)}/repositories`, repositoryRequestOptions(context));
  if (!context.isCurrent() || !validRepositorySet(result, context.manager, todoId)) throw new Error("Invalid repository association response");
  return result;
}
export async function saveTaskRepositories(context: RepositoryRequestContext, todoId: string, revision: number, catalogRevision: number, rows: TaskRepositoryInput[]): Promise<TaskRepositorySet> {
  const result = await api<TaskRepositorySet>(`/todos/${encodeURIComponent(todoId)}/repositories`, {
    ...repositoryRequestOptions(context), method: "PUT", timeout: pastHub(8_000),
    json: { expected_revision: revision, expected_catalog_revision: catalogRevision, repositories: rows },
  });
  if (!context.isCurrent() || !validRepositorySet(result, context.manager, todoId) || result.revision !== revision + 1) throw new Error("Invalid repository association response");
  return result;
}

export function cleanRepositoryDraft(rows: TaskRepositoryInput[]): TaskRepositoryInput[] {
  return rows.map((row) => ({ ...row, links: (row.links ?? []).filter((link) => !("url" in link) || !!link.url?.trim()).map((link) => "url" in link ? { url: link.url!.trim() } : link) }));
}

/** Explicit association creation stays outside the ordinary Todo client's startup path. */
export async function createTodoWithRepositories(context: RepositoryRequestContext, title: string, notes: string, due: { due_at?: string; due_date?: string } | undefined, bindings: CreateRepositoryBindings): Promise<Todo> {
  const result = await api<{ todo: Todo }>("/todos", {
    ...repositoryRequestOptions(context), method: "POST",
    json: { title: title.trim(), notes: notes.trim() || undefined, ...due, repository_bindings: { ...bindings, repositories: cleanRepositoryDraft(bindings.repositories) } },
  });
  if (!context.isCurrent() || !result.todo || typeof result.todo.id !== "string") throw new Error("Invalid Todo creation response");
  return result.todo;
}
