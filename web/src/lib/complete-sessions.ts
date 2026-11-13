// A complete copy of the runtime session collection on a paging hub (#3759, docs/operations/testing.md "Session list reads").
// A paging hub pushes no session rows, only a `sessions_list` hint per change. Views that need every row (pins,
// grouping, per-assistant lists) used to read the whole no-query list again for each hint, hundreds of KB a few times
// a minute; this keeps one baseline of complete pages and then reads only what changed since its revision.
import { api, ApiError } from "./api";
import { t } from "../i18n";
import { runtimeIdentity, runtimeRemoval } from "./runtime-list";
import type { ListPage } from "./session-list";
import type { Session } from "./types";

/** The hub's largest page (docs/protocol/protocol.md "Opt-in parameters"): a few hundred agents are two requests. */
export const COMPLETE_PAGE_LIMIT = 200;

const isPage = (page: Partial<ListPage<Session>>): page is ListPage<Session> =>
  Array.isArray(page.sessions) && Array.isArray(page.remove) && typeof page.revision === "string" && !!page.revision
  && typeof page.has_more === "boolean" && typeof page.total === "number";

const expired = (error: unknown) =>
  error instanceof ApiError && error.status === 409 && ["revision_expired", "cursor_expired"].includes(error.code ?? "");

export interface CompleteRead {
  sessions: Session[];
  /** False when the hub reported no change since the last read: the previous list still holds. */
  changed: boolean;
}

export class CompleteSessions {
  private checkpoint?: string;
  private rows = new Map<string, Session>();
  private list: Session[] = [];

  /** The revision this copy is complete through; a hint carrying it brings nothing new. */
  holds(revision: unknown): boolean {
    return typeof revision === "string" && revision !== "" && revision === this.checkpoint;
  }

  /** The first read, and the one after an expired revision (a hub restart, a long sleep), takes complete pages;
   * every other read only the rows changed or removed since the last one. */
  async read(signal?: AbortSignal): Promise<CompleteRead> {
    if (this.checkpoint !== undefined) {
      try {
        return await this.delta(this.checkpoint, signal);
      } catch (error) {
        if (!expired(error)) throw error;
      }
    }
    return this.baseline(signal);
  }

  private page(params: Record<string, string>, signal?: AbortSignal) {
    const query = new URLSearchParams({ summary: "0", limit: String(COMPLETE_PAGE_LIMIT), ...params });
    return api<Partial<ListPage<Session>>>(`/sessions?${query}`, { signal });
  }

  private async baseline(signal?: AbortSignal): Promise<CompleteRead> {
    const rows = new Map<string, Session>();
    const cursors = new Set<string>();
    let cursor: string | undefined;
    let revision: string | undefined;
    do {
      const page = await this.page(cursor ? { cursor } : {}, signal);
      if (!isPage(page)) {
        // A hub that ignores the paging query answers its whole legacy list: use it, with no checkpoint to follow.
        const legacy = Array.isArray(page.sessions) ? page.sessions : [];
        this.checkpoint = undefined;
        return this.commit(new Map(legacy.map((row) => [runtimeIdentity(row), row])));
      }
      if (revision !== undefined && page.revision !== revision) throw new Error(t("paged-agents.inconsistent_page"));
      revision = page.revision;
      for (const row of page.sessions) rows.set(runtimeIdentity(row), row);
      cursor = page.has_more ? page.next_cursor : undefined;
      if (page.has_more && !cursor) throw new Error(t("paged-agents.invalid_page"));
      // A cursor seen before would page forever: a frozen view's pages each have their own.
      if (cursor && cursors.has(cursor)) throw new Error(t("paged-agents.repeated_cursor"));
      if (cursor) cursors.add(cursor);
    } while (cursor);
    this.checkpoint = revision;
    return this.commit(rows);
  }

  private async delta(since: string, signal?: AbortSignal): Promise<CompleteRead> {
    const changes = new Map<string, Session | null>();
    const cursors = new Set<string>();
    let cursor: string | undefined;
    let target: string | undefined;
    do {
      const page = await this.page({ since, ...(cursor ? { cursor } : {}) }, signal);
      if (!isPage(page)) throw new Error(t("paged-agents.invalid_page"));
      if (target !== undefined && page.revision !== target) throw new Error(t("paged-agents.inconsistent_delta"));
      target = page.revision;
      for (const id of page.remove) changes.set(runtimeRemoval(id), null);
      for (const row of page.sessions) changes.set(runtimeIdentity(row), row);
      cursor = page.has_more ? page.next_cursor : undefined;
      if (page.has_more && !cursor) throw new Error(t("paged-agents.invalid_page"));
      if (cursor && cursors.has(cursor)) throw new Error(t("paged-agents.repeated_cursor"));
      if (cursor) cursors.add(cursor);
    } while (cursor);
    // Only the last page of a delta advances the checkpoint; a failed page leaves it for the next read to redo.
    this.checkpoint = target;
    if (!changes.size) return { sessions: this.list, changed: false };
    const rows = new Map(this.rows);
    for (const [id, row] of changes) {
      if (row === null) rows.delete(id);
      else rows.set(id, row);
    }
    return this.commit(rows);
  }

  private commit(rows: Map<string, Session>): CompleteRead {
    this.rows = rows;
    this.list = [...rows.values()];
    return { sessions: this.list, changed: true };
  }
}
