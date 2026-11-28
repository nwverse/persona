import { useLayoutEffect, useMemo, useState } from "react";
import { t } from "../../i18n";
import { errorText, isAbort } from "../../lib/api";
import {
  bindChannel, channelDetail, forgetChannels, testChannel, unbindChannel,
  type ChannelDetail, type ChannelUpdateBody,
} from "../../lib/channels";
import { ConfigurationController } from "../../lib/configuration/controller";
import { currentAssistant, ownedAssistant } from "../../lib/scoped-assistant-api";
import { toast } from "../../ui/atoms";
import { channelRoutingSource, saveChannelFields } from "./integrations/model";
import { abortedConfiguration, ResourceConfiguration, resourceConfigurations } from "./integrations/resourceConfiguration";
import { useResourceConfiguration } from "./integrations/useResourceConfiguration";
import { useScopedAssistant } from "./integrations/useScopedAssistant";

type TestFeedback = { tone: "ok" | "error"; text: string };
type ChannelRouting = { form: ConfigurationController; start: () => void; reload: () => Promise<void> };

export interface ChannelController {
  detail: ChannelDetail | null;
  error: string;
  loading: boolean;
  busy: string;
  routing?: ChannelRouting;
  testFeedback: TestFeedback | null;
  load: () => Promise<void>;
  bind: (step: string, values: Record<string, string>) => ReturnType<typeof bindChannel>;
  change: (what: string, body: ChannelUpdateBody) => Promise<void>;
  unbind: () => Promise<boolean>;
  runTest: () => Promise<void>;
}

const channels = resourceConfigurations((id, client) => {
  const resource = new ResourceConfiguration(id, client, { read: () => channelDetail(id, client) });
  const form = new ConfigurationController(channelRoutingSource(resource));
  let started = false;
  const routing: ChannelRouting = {
    form,
    start() {
      if (started) return;
      started = true;
      form.start();
    },
    async reload() {
      await resource.refresh();
      if (!resource.snapshot().readError) form.reload();
    },
  };
  return { resource, routing, retire: () => { form.dispose(); resource.retire(); } };
});

const noChannel = async () => {};

/** The channel detail and routing form share the resource even when their hosts remount. */
export function useChannelController(channelId: string | null, manager?: string): ChannelController {
  const { client, routing } = useScopedAssistant(manager === undefined ? currentAssistant : ownedAssistant(manager), "channel", channelId ?? "");
  const entry = channelId ? channels.get(channelId, client) : undefined;
  const resource = entry?.resource;
  const state = useResourceConfiguration(resource, routing);
  const view = useMemo(() => ({ active: false }), [entry]);
  useLayoutEffect(() => {
    view.active = true;
    return () => { view.active = false; };
  }, [view]);
  const [testFeedback, setTestFeedback] = useState<{ view: typeof view; feedback: TestFeedback } | null>(null);
  const notifyError = (error: unknown) => {
    if (view.active && !isAbort(error)) toast(errorText(error), "error");
  };

  const change: ChannelController["change"] = async (label, body) => {
    if (!resource || !view.active) return;
    try {
      if (await saveChannelFields(resource, label, body)) forgetChannels();
    } catch (error) {
      notifyError(error);
    }
  };
  const unbind = async () => {
    if (!resource || !view.active || resource.snapshot().saving) return false;
    try {
      const next = await resource.save({
        label: "unbind",
        prepare: () => ({ send: () => unbindChannel(resource.id, resource.client) }),
      });
      if (next) forgetChannels();
      return !!next && view.active;
    } catch (error) {
      notifyError(error);
      return false;
    }
  };
  const bind: ChannelController["bind"] = (step, values) => {
    if (!resource || !view.active) return Promise.reject(abortedConfiguration());
    return bindChannel(resource.id, step, values, resource.client);
  };
  const runTest = async () => {
    if (!resource || !view.active || resource.snapshot().saving || !resource.snapshot().remote) return;
    setTestFeedback(null);
    try {
      await resource.save({
        label: "test",
        prepare: (remote) => ({
          send: async () => {
            const result = await testChannel(resource.id, resource.client);
            if (!view.active) return;
            const text = result?.note || t(remote.capabilities.reply_only || result?.reply_only
              ? "settings-channels.connection_ok" : "settings-channels.test_sent", { name: remote.name });
            setTestFeedback({ view, feedback: { tone: "ok", text } });
            toast(text, "ok");
          },
        }),
      });
    } catch (error) {
      if (view.active && !isAbort(error)) {
        setTestFeedback({ view, feedback: { tone: "error", text: errorText(error) } });
        notifyError(error);
      }
    }
  };

  return {
    detail: state.remote, error: state.readError ? errorText(state.readError) : "", loading: state.loading,
    busy: state.saving ?? "", routing: entry?.routing, load: resource?.refresh ?? noChannel, bind, change, unbind, runTest,
    testFeedback: testFeedback?.view === view ? testFeedback.feedback : null,
  };
}
