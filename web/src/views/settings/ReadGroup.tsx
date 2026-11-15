// Settings › Hub › Unread › Manual mark as read (#7356): off unless turned on. The switch is the hub's, so every
// browser and the apps show the same (lib/read-setting.ts); on, opening a conversation or an Agent leaves it Unread
// until Mark as read.
import { t } from "../../i18n";
import { errorText } from "../../lib/api";
import { setManualRead, useReadSetting } from "../../lib/read-setting";
import { Spinner, toast } from "../../ui/atoms";
import { Group, Row, Switch } from "./SettingsParts";

export function ReadGroup() {
  const read = useReadSetting();
  const toggle = (on: boolean) => void setManualRead(on).catch((e) => toast(errorText(e), "error"));
  const readonly = read.known && !read.canWrite;
  const hint = !read.supported ? t("settings-manualread.old_hub") : readonly ? t("settings-manualread.readonly") : t("settings-manualread.hint");
  return (
    <Group title={t("settings-manualread.group")} anchor="read-group">
      <Row label={t("settings-manualread.label")} hint={hint} wrapHint>
        {!read.known ? <Spinner /> : <Switch on={read.manual} onChange={toggle} disabled={readonly} label={t("settings-manualread.label")} />}
      </Row>
    </Group>
  );
}
