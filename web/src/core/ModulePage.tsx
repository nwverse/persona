// A feature module's page (core/module-host.ts, docs/web/web.md "Modules"): App draws it for a `module` route. Its code
// and strings load on its first render (loadModule), never while the module is off; the page then draws as any lazy
// page does (lib/lazy-page.ts), with App's Suspense fallback while its chunk is on its way.
import { createElement } from "react";
import { lazyPage, type LazyPageComponent } from "../lib/lazy-page";
import type { Route } from "../lib/router";
import { loadModule, moduleById, moduleState, type ModuleDescriptor, type ModulePageProps } from "./module-host";

// by descriptor: a module registered again (a test) gets a page of its own
const pages = new WeakMap<ModuleDescriptor, LazyPageComponent<ModulePageProps>>();

function pageOf(d: ModuleDescriptor): LazyPageComponent<ModulePageProps> {
  const id = d.id;
  let page = pages.get(d);
  if (!page) {
    page = lazyPage(
      () =>
        loadModule(id).then((code) => {
          if (!code.Page) throw new Error(`module "${id}" has no page`);
          return code.Page;
        }),
      { listed: false },
    );
    pages.set(d, page);
  }
  return page;
}

/** The page of a module route; nothing while its hub has not said whether it serves the module. */
export function ModulePage({ route, onToggleSidebar }: { route: Extract<Route, { name: "module" }>; onToggleSidebar?: () => void }) {
  const d = moduleById(route.module);
  if (!d || moduleState(d) !== "on") return null;
  return createElement(pageOf(d), { path: route.path, onToggleSidebar });
}
