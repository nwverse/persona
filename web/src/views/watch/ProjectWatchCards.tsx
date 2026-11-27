// A project node's routine checks (docs/assistant/watch-cards.md "Where it is shown"): the confirmed watch cards whose
// project is `project` (a knowledge-base project slug), drawn as on Overview, with the same ⋯ actions but no
// reordering (the order is Overview's). Mount it in a project node's detail panel:
//
//   <ProjectWatchCards project={slug} />
//
// It draws nothing while loading, on an older hub, or when the project has no cards (pass `empty` to say so instead).
import { useState } from "react";
import { type Watch } from "../../lib/watches";
import { t } from "../../i18n";
import { Menu, useMenu, useNow } from "../../ui/atoms";
import { WatchCard } from "./WatchCard";
import { WatchEditor } from "./WatchEditor";
import { WATCH_GRID, watchMenuItems } from "./WatchWall";
import { useWatches } from "./useWatches";

export function ProjectWatchCards({ project, empty = false, title = true }: { project: string; /** Say "no routine checks" when there are none. */ empty?: boolean; /** Draw the "Routine checks" heading. */ title?: boolean }) {
  const { watches, error, reload, show } = useWatches(project);
  const now = useNow(60_000);
  const menu = useMenu();
  const [menuFor, setMenuFor] = useState<Watch | null>(null);
  const [editing, setEditing] = useState<Watch | null>(null);
  if (error || !watches || (!watches.length && !empty)) return null;
  return (
    <section data-project-watches={project} aria-label={t("watch.project_title")}>
      {title && <h3 className="mb-2 text-label font-semibold text-ink-3">{t("watch.project_title")}</h3>}
      {watches.length === 0 ? (
        <p className="text-meta text-ink-3">{t("watch.project_empty")}</p>
      ) : (
        <div className={WATCH_GRID}>
          {watches.map((w) => <WatchCard key={w.id} w={w} now={now} where={false} onMenu={(e) => { setMenuFor(w); menu.open(e); }} />)}
        </div>
      )}
      {menu.anchor && menuFor && (
        <Menu anchor={menu.anchor} align="right" onClose={() => { menu.close(); setMenuFor(null); }}
          items={watchMenuItems(menuFor, watches, { show, reload: () => void reload(), edit: setEditing, reorder: false })} />
      )}
      {editing && <WatchEditor w={editing} onClose={() => setEditing(null)} onSaved={() => void reload()} />}
    </section>
  );
}

export default ProjectWatchCards;
