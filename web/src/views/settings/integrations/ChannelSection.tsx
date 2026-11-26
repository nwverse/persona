// Channel state and unified sections (Account & Connection binding + Chat & Delivery routing) for IntegrationDetail (#14847).
import { useMemo, useRef } from "react";
import { t } from "../../../i18n";
import type { ChannelDetail } from "../../../lib/channels";
import { Loading } from "../../../ui/atoms";
import { ChannelBindingBlock, ChannelDeliveryControls, ChannelStateInline } from "../Channels";
import { ConfigurationForm } from "../configuration/ConfigurationForm";
import { Group, SettingsHint } from "../SettingsParts";
import { useChannelController, type ChannelController } from "../useChannelController";
import { channelRoutingSource } from "./model";

export { ChannelBindingBlock, ChannelStateInline, useChannelController, type ChannelController };

/** Chat & Delivery controls: enable, target, thread_mode & thread_idle_hours, features, capabilities, test message. */
export function ChannelDeliveryBlock({ ctl, onSettings }: { ctl: ChannelController; onSettings?: (url: string) => void }) {
  const detailRef = useRef<ChannelDetail | null>(ctl.detail);
  detailRef.current = ctl.detail;

  const d = ctl.detail;
  const routingSource = useMemo(() => {
    if (!d) return null;
    return channelRoutingSource(
      d.id,
      ctl.reloadKey,
      () => detailRef.current ?? d,
      ctl.adopt,
    );
    // Keep the ConfigurationForm controller mounted across its own saves; only remount on channel change or explicit reload.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [d?.id, ctl.reloadKey, ctl.adopt]);

  if (!d) {
    if (ctl.error) {
      return (
        <Group>
          <SettingsHint alert>{t("settings-integrations.channel_unavailable", { error: ctl.error })}</SettingsHint>
        </Group>
      );
    }
    if (ctl.loading) {
      return (
        <Group>
          <div className="px-4 py-3"><Loading /></div>
        </Group>
      );
    }
    return null;
  }

  return (
    <ChannelDeliveryControls
      ctl={ctl}
      onSettings={onSettings}
      afterTarget={
        routingSource && d.kind !== "app" ? (
          <div className="mb-6" data-channel-routing>
            <ConfigurationForm source={routingSource} />
          </div>
        ) : undefined
      }
    />
  );
}
