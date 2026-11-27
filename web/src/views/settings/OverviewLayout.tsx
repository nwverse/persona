// Settings › General › Overview (#13019, docs/web/web.md "Overview by project"): the classic layout's switch. Off by
// default: Overview shows the projects; on, the sections in columns as before. Kept in this browser (lib/overview-layout.ts).
import { t } from "../../i18n";
import { setClassicOverview, useClassicOverview } from "../../lib/overview-layout";
import { Group, Row, Switch } from "./SettingsParts";

export function OverviewLayoutGroup() {
  const classic = useClassicOverview();
  return (
    <Group anchor="overview" title={t("overview-projects.settings_title")} footer={t("overview-projects.settings_footer")}>
      <Row label={t("overview-projects.settings_classic")} hint={t("overview-projects.settings_classic_hint")} wrapHint anchor="overview-classic">
        <Switch on={classic} onChange={setClassicOverview} label={t("overview-projects.settings_classic")} />
      </Row>
    </Group>
  );
}
