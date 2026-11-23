// The health module's descriptor (#13434, the web pilot of #13430; docs/web/web.md "Modules", docs/assistant/health.md):
// what it adds to the shell, small enough to load at start. Its page and its strings (health.json) load on first use.
//
// No hub feature flag: every hub this web app talks to has served the health space, and before this module the page
// was always there. So it names none and stays on by default; Settings › Labs › Features turns it off in a browser.
import { byLanguage, t, type CatalogModule } from "../../i18n";
import type { ModuleDescriptor } from "../../core/module-host";

export const health: ModuleDescriptor = {
  id: "health",
  title: () => t("sidebar.nav_health"),
  description: () => t("sidebar.nav_health_hint"),
  routes: [{ base: "health", aliases: [["tracks", "health"]], title: () => t("app.title_health") }],
  // right after Inbox, where it always was
  navItems: [{ key: "health", path: "/health", icon: "heart", label: () => t("sidebar.nav_health"), short: () => t("sidebar.nav_health_short"), color: "pink", after: "inbox" }],
  i18nNamespaces: ["health"],
  locales: byLanguage(import.meta.glob<CatalogModule>("../../i18n/locales/*/_meta.json", { query: "?catalog=module-health" })),
  load: () => import("./Health").then((m) => ({ Page: m.HealthPage })),
};
