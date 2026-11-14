// Inbox (docs/assistant/inbox.md): Feishu messages, mail, Feishu documents and Moments in one list, sorted by the AI's triage,
// with the agents waiting on you at the head of "Needs you". Wide: the list (380px) and the opened item side by side;
// narrow: the opened item takes the list's place, with a way back. "Select" (top right) picks several items to mark them
// done at once (#2059).
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { TodoDialogHost } from "./InboxTodoDialog";
import { useInbox, refreshInbox, loadRoutedItem, type InboxItem } from "../../lib/inbox";
import { useScope } from "../../lib/current-assistant";
import { ItemManager, otherManager, sameManager } from "../../lib/manager-choice";
import { newDraft, useMailAccounts } from "../../lib/mail";
import { backToInboxList } from "../../lib/inbox-nav";
import { InboxViewMemoryContext, useInboxViewMemory } from "../../lib/inbox-view-memory";
import { t } from "../../i18n";
import { Button, cx, IconButton, Spinner } from "../../ui/atoms";
import { ComposeDialog } from "./ComposeDialog";
import { DetailPlaceholder, InboxDetail } from "./InboxDetail";
import { InboxList } from "./InboxList";
import { PolicyDialogHost } from "./PolicyDialog";
import { FolderDialogHost } from "./InboxFolders";

/** The item a route opens: its id of its manager (none: the default's). A link that names no manager (from before
 *  several managers, or written by hand) opens the default's copy, else the one manager that has it. */
export function routedItem(items: InboxItem[], id: string | undefined, manager: string | undefined): InboxItem | undefined {
  if (!id) return undefined;
  const same = items.filter((x) => x.id === id);
  return same.find((x) => sameManager(x.manager, manager)) ?? (manager ? undefined : same[0]);
}

export function Inbox({ id, manager, onToggleSidebar }: { id?: string; manager?: string; onToggleSidebar: () => void }) {
  const view = useInboxViewMemory(id, manager);
  // the list's copy, else Done's history's: the list carries only the newest done items (#5549)
  const item = useInbox((s) => routedItem(s.items, id, manager) ?? routedItem(s.history, id, manager));
  const loaded = useInbox((s) => s.loaded);
  const loading = useInbox((s) => s.loading);
  // neither has it (a link to an item done past what the list carries): the hub's copy, once
  const routing = useInbox((s) => s.routing.length > 0);
  useEffect(() => {
    if (id && loaded && !item) loadRoutedItem(id, manager);
  }, [id, manager, loaded, item]);
  // a new mail is the current assistant's (docs/assistant/managers.md "Clients"): its own mail accounts, sent through it
  const scope = useScope();
  const composer = scope.several ? otherManager(scope.id) : undefined;
  const accounts = useMailAccounts({ manager: composer });
  const [composing, setComposing] = useState(false);
  // Selecting (#2059): the picked items' keys (itemKey); null: not selecting. Esc leaves it.
  const [picked, setPicked] = useState<Set<string> | null>(null);
  const picking = picked !== null;
  useEffect(() => {
    if (!picking) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !e.defaultPrevented) setPicked(null);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [picking]);
  // Select and Done are one button, so the keyboard stays on it as it turns (audit item 71); when selecting ends from
  // elsewhere (Esc on a row's tick, Mark as done in the bar), what had the focus is gone, and it comes back here.
  const selectButton = useRef<HTMLButtonElement>(null);
  useFocusWhenLost(picking, () => selectButton.current);
  // On a narrow page the opened item has the list's place (audit item 74): the focus that went with the row opening it
  // moves to the item, and back to that row when ‹, Esc or the browser's Back returns to the list.
  const listBox = useRef<HTMLElement>(null);
  const detailBox = useRef<HTMLElement>(null);
  const opened = useRef(id ? { id, manager } : undefined);
  useFocusWhenLost(id ?? "", () => {
    const was = opened.current;
    opened.current = id ? { id, manager } : undefined;
    return id ? detailBox.current : was ? openedRow(listBox.current, was) : null;
  }, true);
  // Esc there goes back to the list as ‹ does: not over a dialog or a menu, nor in a field (Esc is its own there), and not
  // on a wide page, where the list is beside the item
  useEffect(() => {
    if (!id) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.defaultPrevented || e.isComposing || shown(listBox.current)) return;
      if (document.querySelector("[role=dialog], [role=menu]") || (e.target instanceof Element && e.target.closest("input, textarea, select, [contenteditable]"))) return;
      backToInboxList();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [id]);
  return (
    <InboxViewMemoryContext.Provider value={view}>
    <div className="@container flex h-full flex-col">
      <header className="flex h-14 shrink-0 items-center gap-2 border-b border-line px-3 sm:px-4">
        <IconButton icon="sidebar" label={t("inbox-inbox.sidebar")} onClick={onToggleSidebar} className="lg:hidden" />
        {id && <IconButton icon="chevronLeft" label={t("inbox-inbox.back_to_inbox")} onClick={backToInboxList} className="@3xl:hidden" />}
        <h1 className="min-w-0 flex-1 truncate text-[14px] font-semibold text-ink">{t("inbox-inbox.title")}</h1>
        {loading && loaded && <Spinner />}
        {/* the list's: not over an opened item that has the list's place */}
        <Button ref={selectButton} tone="quiet" onClick={() => setPicked(picking ? null : new Set())} className={cx(!picking && id && "@max-3xl:hidden")}>
          {picking ? t("inbox-inbox.select_done") : t("inbox-inbox.select")}
        </Button>
        {!picking && (
          <>
            <IconButton icon="compose" label={accounts.length ? t("inbox-inbox.compose") : t("inbox-inbox.compose_no_account")} onClick={() => setComposing(true)} disabled={!accounts.length} />
            <IconButton icon="retry" label={t("inbox-inbox.refresh")} onClick={() => void refreshInbox()} disabled={loading} />
          </>
        )}
      </header>
      <div className="flex min-h-0 flex-1">
        <section ref={listBox} aria-label={t("inbox-inbox.list")} className={cx("min-h-0 w-full flex-col @3xl:flex @3xl:w-[380px] @3xl:shrink-0 @3xl:border-r @3xl:border-line", id ? "hidden" : "flex")}>
          <InboxList key={view.revision} selected={item ?? (id ? { id, manager } : undefined)} picked={picked} onPicked={setPicked} />
        </section>
        <section ref={detailBox} tabIndex={-1} aria-label={t("inbox-inbox.detail")} className={cx("min-h-0 min-w-0 flex-1 flex-col outline-none @3xl:flex", id ? "flex" : "hidden")}>
          {id ? <InboxDetail item={item} loaded={loaded && !routing} /> : <DetailPlaceholder />}
        </section>
      </div>
      <PolicyDialogHost />
      <FolderDialogHost />
      <TodoDialogHost />
      {composing && (
        <ItemManager.Provider value={composer}>
          <ComposeDialog draftKey="new" initial={newDraft(accounts[0]?.id ?? "")} accounts={accounts} onClose={() => setComposing(false)} />
        </ItemManager.Provider>
      )}
    </div>
    </InboxViewMemoryContext.Provider>
  );
}

/** When `key` changes and the focus was lost with what changed, it goes to `target()`. Lost: on the page's body (what
 *  had it was removed), or on something no longer drawn (hidden with its list; the browser moves such a focus to the
 *  body only at its next frame). A focus still in sight (a row beside the item on a wide page) stays. `reveal`: brought
 *  into sight in its own scroller, as the list shows the opened row. */
function useFocusWhenLost(key: unknown, target: () => HTMLElement | null | undefined, reveal = false): void {
  const last = useRef(key);
  useLayoutEffect(() => {
    if (last.current === key) return;
    last.current = key;
    const el = target();
    const at = document.activeElement;
    if (!el || (at instanceof HTMLElement && at !== document.body && shown(at))) return;
    el.focus({ preventScroll: true });
    // only one that took it: a row filed in a folded section (Done) can't, and the list stays where it was
    if (reveal && document.activeElement === el) el.scrollIntoView?.({ block: "nearest" });
  });
}

/** The row of the item `it` in the list, as its link (or its tick while selecting). */
function openedRow(list: HTMLElement | null, it: { id: string; manager?: string }): HTMLElement | null {
  const rows = [...(list?.querySelectorAll<HTMLElement>(`[data-item="${CSS.escape(it.id)}"]`) ?? [])];
  const row = rows.find((el) => sameManager(el.dataset.manager, it.manager)) ?? rows[0];
  return row?.querySelector<HTMLElement>(":scope > a, :scope > [role=checkbox]") ?? null;
}

/** Whether `el` is drawn (the list on a wide page, hidden while an opened item has its place on a narrow one; what is
 *  in either). */
function shown(el: HTMLElement | null): boolean {
  return Boolean(el && (el.checkVisibility ? el.checkVisibility() : el.getClientRects().length > 0));
}
