// The recording module's descriptor (#13407, web half; #13430; docs/web/web.md "Modules", docs/assistant/meeting-minutes.md,
// docs/assistant/day-recap.md): the meeting recording, minutes and day-recap parts of a recording's knowledge page, through
// the `kb.detail` slot. Its code and its strings (recording.json, recording-*.json) load when a recording opens.
//
// No hub feature flag yet: every hub this web app talks to has served recordings, and before this module their pages
// were always there. So it names none and stays on by default; Settings › Labs › Features turns it off in a browser,
// and then recordings are absent from Knowledge (the Meetings list, their addresses).
import { byLanguage, t, type CatalogModule } from "../../i18n";
import type { ModuleDescriptor } from "../../core/module-host";

export const recording: ModuleDescriptor = {
  id: "recording",
  title: () => t("sidebar.module_recording"),
  description: () => t("sidebar.module_recording_hint"),
  kbKinds: ["recordings"],
  i18nNamespaces: ["recording"],
  locales: byLanguage(import.meta.glob<CatalogModule>("../../i18n/locales/*/_meta.json", { query: "?catalog=module-recording" })),
  load: () => import("./RecordingDetail").then((m) => ({ KBDetail: m.RecordingDetail })),
};
