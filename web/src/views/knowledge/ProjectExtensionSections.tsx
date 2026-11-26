// Installed extensions on a project's detail page. Listing their declarations never calls an extension.
// A deep link must match this manager's live list before the renderer gets the project identifier.
import { Suspense, useEffect, useState } from "react";
import { t } from "../../i18n";
import { ApiError } from "../../lib/api";
import { hasSlots } from "../../lib/extension-slots";
import { ExtensionOwnerScope } from "../../lib/extension-owner";
import { EXTENSIONS_CHANGED } from "../../lib/extension-prompts";
import { uiPlaces, type UIPlace } from "../../lib/extension-ui";
import { lazyPage } from "../../lib/lazy-page";
import { href, navigate, onLinkClick, useRoute, type Route } from "../../lib/router";
import { onHubEvent, useStore } from "../../lib/store";
import { IconButton, Loading } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { Card, SectionTitle } from "./KBParts";

const ExtensionUI = lazyPage(() => import("../settings/ExtensionView").then(m => m.ExtensionUI));

export function ProjectExtensionSections({ project, manager }: { project: string; manager: string }) {
  const route = useRoute();
  const selected = route.name === "kb" && route.kind === "projects" && route.id === project ? route.section : undefined;
  const supported = useStore(s => s.features.length ? hasSlots(s.features) : undefined);
  const [places, setPlaces] = useState<UIPlace[] | null>(null);
  const [missing, setMissing] = useState<string>();
  useEffect(() => {
    if (!supported) return;
    let request = 0;
    const load = () => {
      const mine = ++request;
      uiPlaces("project_section", undefined, undefined, manager).then(list => {
        if (request !== mine) return;
        setMissing(undefined);
        setPlaces(list.filter(p => p.placement === "project_section" && p.extension && p.id));
      }, e => {
        if (request === mine) setPlaces(e instanceof ApiError && e.status === 404 ? [] : null);
      });
    };
    load();
    const reconnect = onHubEvent("hello", load);
    const changes = onHubEvent("extensions", load);
    window.addEventListener(EXTENSIONS_CHANGED, load);
    return () => {
      request++;
      reconnect();
      changes();
      window.removeEventListener(EXTENSIONS_CHANGED, load);
    };
  }, [manager, supported]);

  const base: Route = { name: "kb", kind: "projects", id: project };
  const available = supported ? places : null;
  const placeKey = (p: UIPlace) => JSON.stringify([p.extension, p.id]);
  const opened = available?.find(p => p.extension === selected?.extension && p.id === selected?.ui && placeKey(p) !== missing);
  useEffect(() => {
    if (selected && !opened && (supported === false || available !== null)) {
      navigate({ name: "kb", kind: "projects", id: project }, true);
    }
  }, [selected?.extension, selected?.ui, opened, available, supported, project]);
  if (!available?.length) return null;
  const visible = available.filter(p => placeKey(p) !== missing);
  if (!visible.length) return null;
  return (
    <section aria-label={t("knowledge-kbdetail.project_extensions")}>
      <SectionTitle title={t("knowledge-kbdetail.project_extensions")} />
      <Card>
        {visible.map(p => {
          const link: Route = { ...base, section: { extension: p.extension, ui: p.id } };
          return <div key={placeKey(p)} className="flex items-center">
            <a href={href(link)} onClick={event => onLinkClick(event, link)} aria-current={p === opened ? "page" : undefined}
              className="flex min-h-11 min-w-0 flex-1 items-center gap-2 rounded-control px-3 py-2 text-meta text-ink hover:bg-hover focus-visible:outline-2 focus-visible:outline-accent">
              <Icon name="extension" size={16} />
              <span className="min-w-0 flex-1 break-words">{p.title}</span>
              {p.extension_name && p.extension_name !== p.title && <span className="truncate text-submeta text-ink-2">{p.extension_name}</span>}
              {p !== opened && <Icon name="chevronRight" size={16} />}
            </a>
            {p === opened && <IconButton icon="x" label={t("knowledge-kbdetail.close_section")} onClick={() => navigate(base, true)} />}
          </div>;
        })}
        {opened && <div className="border-t border-line p-2">
          <Suspense fallback={<Loading />}>
            <ExtensionOwnerScope owner={opened.extension}>
              <ExtensionUI key={placeKey(opened)} extension={opened.extension} ui={opened.id} context={{ project_id: project }} manager={manager}
                embedded bare title={opened.title} onUnsupported={() => setMissing(placeKey(opened))} onMissing={() => setMissing(placeKey(opened))} />
            </ExtensionOwnerScope>
          </Suspense>
        </div>}
      </Card>
    </section>
  );
}
