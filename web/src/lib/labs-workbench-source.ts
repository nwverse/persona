// Settings › Labs' Workbench layout module (#13439): the switch (lib/labs-workbench.ts) as a configuration Document, so the
// Labs page draws it with the generic form. Apart from the store so its words stay out of the first screen: only Settings
// loads this file. Same rules as lib/labs-terminal.ts: an update names this Document and the revision it was made against,
// is checked here, and is answered with the Document as committed.
import { t } from "../i18n";
import { checkUpdate, CONFIGURATION_VERSION, ConfigurationError, type ConfigurationDocument, type ConfigurationSource, type ConfigurationUpdate } from "./configuration/contract";
import type { LabState } from "./labs";
import { readWorkbench, subscribeWorkbench, writeWorkbench } from "./labs-workbench";

export const WORKBENCH_LABS_ID = "workbench";
const FIELD = "enabled";

const revision = (state: LabState<boolean>) => (state.source === "unreadable" ? "unreadable" : state.raw === null ? "default" : `saved:${state.raw}`);

function document(): ConfigurationDocument {
  const state = readWorkbench();
  const notice = state.source === "invalid" ? t("settings-labs.invalid") : state.source === "unreadable" ? t("settings-labs.unreadable") : "";
  return {
    schema_version: CONFIGURATION_VERSION,
    id: WORKBENCH_LABS_ID,
    revision: revision(state),
    title: t("settings-labs.area_agents"),
    groups: [
      {
        id: "workbench",
        title: t("settings-labs.workbench_group"),
        fields: [
          {
            id: FIELD,
            kind: "toggle",
            title: t("settings-labs.workbench_enabled"),
            description: t("settings-labs.workbench_enabled_hint"),
            default: false,
            details: [
              { title: t("settings-labs.scope"), description: t("settings-labs.scope_browser") },
              { title: t("settings-labs.activation"), description: t("settings-labs.workbench_activation") },
              { title: t("settings-labs.rollback"), description: t("settings-labs.workbench_rollback") },
            ],
          },
        ],
      },
    ],
    values: { [FIELD]: state.value },
    ...(notice ? { notices: { [FIELD]: { kind: "warning", message: notice } } } : {}),
  };
}

function update(u: ConfigurationUpdate): ConfigurationDocument {
  const refused = checkUpdate(document(), u);
  if (refused) throw refused;
  const next = u.reset?.includes(FIELD) ? false : u.changes?.[FIELD] === true;
  if (!writeWorkbench(next).ok) throw new ConfigurationError("storage_failed", t("settings-labs.save_failed"));
  return document();
}

/** The Workbench's Labs source: reads and writes this browser's choice, and follows other tabs. */
export function workbenchLabsSource(): ConfigurationSource {
  return {
    key: `labs:${WORKBENCH_LABS_ID}`,
    read: async () => document(),
    update: async (u) => update(u),
    subscribe: subscribeWorkbench,
  };
}
