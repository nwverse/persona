import { t } from "../../i18n";
import { TAB_INFO, TABS, type Tab } from "./nav";

// ── search ──────────────────────────────────────────────

/** A group (or a models/agents section), or one setting's row, inside a page that the search box can jump to. */
export interface SearchGroup {
  tab: Tab;
  /** The group's heading or the row's label, as the page shows it. */
  group: string;
  /** The `data-settings-group` marker of a Group, the `data-settings-row` marker of a row, or the element id of a
   *  models/agents section. */
  anchor: string;
  /** More words it is found by (the choices a row offers), never shown. */
  hint?: string;
}

/** What the search box matches beyond the categories (a curated static index): the pages' group headings and, by their
 *  labels, the settings on them (audit item 18), each in the page's own words, so every language finds them. */
export const SEARCH_INDEX: SearchGroup[] = [
  // General
  { tab: "general", group: t("settings.appearance"), anchor: "appearance" },
  { tab: "general", group: `${t("settings.theme_light")} / ${t("settings.theme_dark")}`, hint: t("language.system"), anchor: "theme" },
  { tab: "general", group: t("ui-look.accent"), anchor: "theme-color" },
  { tab: "general", group: t("ui-look.text_size"), anchor: "text-size" },
  { tab: "general", group: t("language.label"), anchor: "language" },
  { tab: "general", group: t("settings.reply_motion"), anchor: "reply-motion" },
  { tab: "general", group: t("ui-look.recent_chats"), anchor: "recent-chats" },
  { tab: "general", group: t("settings.sidebar_pages"), anchor: "sidebar-pages" },
  { tab: "general", group: t("settings-nav.tab_translate"), anchor: "settings-translation" },
  // Persona
  { tab: "general", group: t("settings.agent_preferences"), anchor: "agent-preferences" },
  { tab: "general", group: t("settings-agentopen.label"), hint: `${t("settings-agentopen.panel")} / ${t("settings-agentopen.page")}`, anchor: "agent-open" },
  { tab: "general", group: t("context.details"), anchor: "agent-details" },
  { tab: "general", group: t("context.as_used"), anchor: "context-as-used" },
  { tab: "general", group: t("hubs.group_title"), hint: t("hubs.add_hub"), anchor: "hubs" },
  { tab: "general", group: t("settings.this_browser"), anchor: "this-browser" },
  { tab: "general", group: t("settings.hub_address"), anchor: "hub-address" },
  { tab: "general", group: t("settings.install_app"), anchor: "install-app" },
  { tab: "general", group: t("settings.disconnect_browser"), hint: t("disconnect.sign_out"), anchor: "disconnect" },
  { tab: "general", group: t("settings.source_code"), anchor: "about-persona" },
  // the assistant's profile
  { tab: "persona", group: t("settings-triagesettings.title"), anchor: "triage" },
  // the hub's
  { tab: "avatar", group: t("settings.avatar"), anchor: "avatar" },
  { tab: "hub", group: t("workspace-board.setting_group"), anchor: "agent-group" },
  { tab: "persona", group: t("settings-standing.title"), anchor: "standing-authorizations" },
  // Models' sections and Agents' sections (their element ids)
  { tab: "models", group: t("settings-nav.tab_providers"), anchor: "settings-providers" },
  // a provider's key is "API Key" in every language
  { tab: "models", group: t("settings.add_provider"), hint: `API Key / ${t("settings-providerimport.import")}`, anchor: "settings-providers" },
  { tab: "models", group: t("settings-nav.tab_model_config"), anchor: "settings-model-config" },
  { tab: "agents", group: "Claude Code", anchor: "settings-claude" },
  { tab: "agents", group: "Codex", anchor: "settings-codex" },
  { tab: "agents", group: "Antigravity", anchor: "settings-agy" },
  { tab: "agents", group: "omp", anchor: "settings-omp" },
  { tab: "knowledge", group: t("settings-knowledge.topics_title"), anchor: "knowledge-topics" },
  { tab: "knowledge", group: t("settings-knowledge.items"), anchor: "knowledge-items" },
  { tab: "knowledge", group: t("settings-knowledge.messages"), anchor: "knowledge-messages" },
  { tab: "knowledge", group: t("settings-knowledge.docs"), anchor: "knowledge-docs" },
  // Notifications
  { tab: "notifications", group: t("settings-notifications.system_title"), anchor: "system" },
  { tab: "notifications", group: t("settings-notifications.system"), anchor: "notify-system" },
  { tab: "notifications", group: t("settings-notifications.sound"), anchor: "notify-sound" },
  { tab: "notifications", group: t("settings-notifications.done_title"), anchor: "done" },
  // Connectors
  { tab: "connectors", group: t("settings-connectors.mail"), anchor: "mail" },
  { tab: "connectors", group: t("settings-connectors.lark"), anchor: "lark" },
  { tab: "connectors", group: t("settings-sent.entry_label"), hint: t("settings-sent.hint"), anchor: "sent" },
  // Extensions
  { tab: "extensions", group: t("settings-extensions.title"), anchor: "extensions" },
  { tab: "extensions", group: t("settings-extensions.market_title"), anchor: "market" },
  // Shortcuts / hub / devices
  { tab: "shortcuts", group: t("settings-shortcutsettings.enable_all"), anchor: "shortcuts-all" },
  { tab: "shortcuts", group: t("settings-shortcutsettings.terminal_title"), anchor: "shortcuts-terminal" },
  { tab: "hub", group: t("settings-hubsettings.version"), anchor: "hub-version" },
  { tab: "hub", group: t("settings-hubsettings.update"), anchor: "hub-update" },
  { tab: "hub", group: t("workspace-board.setting_label"), anchor: "workflow-board" },
  { tab: "hub", group: t("settings-hubsettings.mirrors"), anchor: "mirrors" },
  { tab: "devices", group: t("settings-connectdevice.add"), hint: t("settings.revoke"), anchor: "devices" },
];

/** The search box's matches: categories by label / hint, and page groups and settings by their heading or label. */
export function searchSettings(query: string): { tabs: Tab[]; groups: SearchGroup[] } {
  const q = query.trim().toLocaleLowerCase();
  if (!q) return { tabs: [], groups: [] };
  return {
    tabs: TABS.filter((t) => (TAB_INFO[t].label + " " + TAB_INFO[t].hint).toLocaleLowerCase().includes(q)),
    groups: SEARCH_INDEX.filter((g) => (g.group + " " + (g.hint ?? "")).toLocaleLowerCase().includes(q)),
  };
}
