// Overview's watch wall (docs/assistant/watch-cards.md, #13020): the cards the owner asked Persona to keep checking, at
// the top of Overview in the owner's order. Drafts the assistant prepared wait here too, with Confirm and Discard. Each
// confirmed card's ⋯ refreshes it now, pauses or resumes it, edits it, moves it earlier or later, or deletes it (with
// Undo for nothing: deleting asks first). Cards are dragged by their grip to reorder. With no cards, the wall says how
// to get one in a line and takes no more room.
import { useState } from "react";
import { errorText } from "../../lib/api";
import { confirmWatch, deleteWatch, droppedOn, moved, refreshWatch, reorderWatches, updateWatch, type Watch } from "../../lib/watches";
import { t } from "../../i18n";
import { Menu, toast, useMenu, useNow } from "../../ui/atoms";
import type { MenuItem } from "../../ui/menu-model";
import { WatchCard } from "./WatchCard";
import { WatchEditor } from "./WatchEditor";
import { useWatches } from "./useWatches";

/** The card grid: as many columns of at least 240 px as fit. */
export const WATCH_GRID = "grid items-start gap-3 [grid-template-columns:repeat(auto-fill,minmax(min(100%,240px),1fr))]";

/** The actions of a confirmed card, shared by the wall and a project's panel. */
export function watchMenuItems(w: Watch, list: Watch[], o: { show: (ws: Watch[]) => void; reload: () => void; edit: (w: Watch) => void; reorder: boolean }): MenuItem[] {
  const ids = list.map((x) => x.id);
  const fail = (e: unknown) => {
    toast(errorText(e), "error");
    o.reload();
  };
  const move = (by: number) => {
    const next = moved(ids, w.id, by);
    o.show(next.map((id) => list.find((x) => x.id === id)!));
    reorderWatches(next).catch(fail);
  };
  const items: MenuItem[] = [
    { key: "refresh", label: t("watch.refresh"), icon: "retry", disabled: !w.task_id || w.paused || w.no_task, onSelect: () => refreshWatch(w).then(() => toast(t("watch.refreshing"), "ok"), fail) },
    w.paused || w.no_task
      ? { key: "resume", label: t("watch.resume"), icon: "play", onSelect: () => updateWatch(w.id, { paused: false }).then(o.reload, fail) }
      : { key: "pause", label: t("watch.pause"), icon: "pause", onSelect: () => updateWatch(w.id, { paused: true }).then(o.reload, fail) },
    { key: "edit", label: t("watch.edit"), icon: "pencil", onSelect: () => o.edit(w) },
  ];
  if (o.reorder) {
    const i = ids.indexOf(w.id);
    items.push(
      { key: "earlier", label: t("watch.move_earlier"), icon: "arrowLeft", disabled: i <= 0, divider: true, onSelect: () => move(-1) },
      { key: "later", label: t("watch.move_later"), icon: "arrowRight", disabled: i >= ids.length - 1, onSelect: () => move(1) },
    );
  }
  items.push({
    key: "delete", label: t("watch.delete"), icon: "trash", danger: true, divider: true, confirm: t("watch.delete_confirm"),
    confirmDetail: t("watch.delete_detail"),
    onSelect: () => {
      o.show(list.filter((x) => x.id !== w.id));
      return deleteWatch(w.id).catch(fail);
    },
  });
  return items;
}

export default function WatchWall() {
  const { watches, error, reload, show } = useWatches();
  const now = useNow(60_000);
  const menu = useMenu();
  const [menuFor, setMenuFor] = useState<Watch | null>(null);
  const [editing, setEditing] = useState<Watch | null>(null);
  const [dragging, setDragging] = useState<string | null>(null);
  const [over, setOver] = useState<string | null>(null);
  if (error || !watches) return null; // an older hub has no watch cards; Overview is as it was
  const fail = (e: unknown) => {
    toast(errorText(e), "error");
    void reload();
  };
  const alerts = watches.filter((w) => w.state === "active" && w.latest?.alert).length;
  const ids = watches.map((w) => w.id);
  const drop = (to: string) => {
    if (!dragging) return;
    const next = droppedOn(ids, dragging, to);
    setDragging(null);
    if (next === ids) return;
    show(next.map((id) => watches.find((w) => w.id === id)!));
    reorderWatches(next).catch(fail);
  };
  return (
    <section aria-labelledby="watch-wall-title" data-watch-wall>
      <div className="mb-3 flex flex-wrap items-baseline gap-x-2">
        <h2 id="watch-wall-title" className="text-card-title font-semibold text-ink">{t("watch.wall_title")}</h2>
        {watches.length > 0 && (
          <span className="text-meta text-ink-3">
            {t("watch.count", { count: watches.filter((w) => w.state === "active").length })}
            {alerts > 0 && <span className="text-red"> · {t("watch.alert_count", { count: alerts })}</span>}
          </span>
        )}
      </div>
      {watches.length === 0 ? (
        <p className="text-meta text-ink-3" data-watch-wall-empty>{t("watch.wall_empty")}</p>
      ) : (
        <div className={WATCH_GRID}>
          {watches.map((w) => (
            <WatchCard
              key={w.id}
              w={w}
              now={now}
              onConfirm={w.state === "draft" ? () => void confirmWatch(w.id).then(() => { toast(t("watch.confirmed"), "ok"); void reload(); }, fail) : undefined}
              onDiscard={w.state === "draft" ? () => { show(watches.filter((x) => x.id !== w.id)); deleteWatch(w.id).catch(fail); } : undefined}
              onMenu={(e) => { setMenuFor(w); menu.open(e); }}
              drag={{ onDragStart: () => setDragging(w.id), onDrop: () => drop(w.id), over: over === w.id && dragging !== w.id, setOver: (on) => setOver(on ? w.id : null) }}
            />
          ))}
        </div>
      )}
      {menu.anchor && menuFor && (
        <Menu anchor={menu.anchor} align="right" onClose={() => { menu.close(); setMenuFor(null); }}
          items={watchMenuItems(menuFor, watches, { show, reload: () => void reload(), edit: setEditing, reorder: true })} />
      )}
      {editing && <WatchEditor w={editing} onClose={() => setEditing(null)} onSaved={() => void reload()} />}
    </section>
  );
}
