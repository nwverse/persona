import { t } from "../../i18n";
import { setBusySend, useBusySend, type BusySend } from "../../lib/busy-send";
import { Segmented } from "../assistant/parts";
import { Row } from "./SettingsParts";

export function BusySendRow() {
  const value = useBusySend();
  const label = t("settings-agentopen.busy_send");
  return (
    <Row label={label} hint={t("settings-agentopen.busy_send_hint")} wrapHint stackOnNarrow anchor="busy-send">
      <Segmented<BusySend> label={label} value={value} onChange={setBusySend} options={[
        { v: "queue", label: t("settings-agentopen.busy_queue") },
        { v: "follow_up", label: t("settings-agentopen.busy_follow_up") },
      ]} />
    </Row>
  );
}
