// The structured details an Agent row shows on hover or keyboard focus (#9769). The tooltip and its content load on intent.
import { useEffect, useRef } from "react";
import { t } from "../i18n";
import { sessionHost, sessionSource, sourceName } from "../lib/agent-groups";
import { sessionAgentName } from "../lib/agent-kind";
import { modelShort } from "../lib/context-usage";
import { engineName } from "../lib/engines";
import { ago, sessionTitle, tabLabel } from "../lib/format";
import type { Machine, Session } from "../lib/types";
import { TIP_DELAY, useTips } from "./TodoRequestGraphTip";

export default function SessionRowDetails({ id, anchor, keyboard, onClose, ...props }: {
  id: string;
  anchor: Element;
  keyboard: boolean;
  onClose: () => void;
  s: Session;
  machine?: Machine;
  machineName: string;
  stateLabel: string;
}) {
  const { api, layer } = useTips(id, onClose);
  // The card follows the row's intent (the mount: the pointer entered or focus landed), not its content. The card reads
  // the props through this ref, so a session update while it is up rewrites it in place instead of hiding it and
  // waiting out the delay again.
  const latest = useRef(props);
  latest.current = props;
  useEffect(() => {
    api.show(anchor, () => <Details {...latest.current} />, keyboard ? 0 : TIP_DELAY);
    return () => api.hide(anchor);
  }, [api, id, anchor, keyboard]);
  // `layer` is the card while it is up: a content change then shows it again at once, at the same anchor, so it keeps
  // its place. Before it is up the pending open keeps its delay; a late change reaches the card through the ref.
  const up = useRef(false);
  up.current = layer !== null;
  useEffect(() => {
    if (up.current) api.show(anchor, () => <Details {...latest.current} />, 0);
  }, [api, anchor, props.s, props.machine, props.machineName, props.stateLabel]);
  return layer;
}

function Details({ s, machine, machineName, stateLabel }: { s: Session; machine?: Machine; machineName: string; stateLabel: string }) {
  const source = sessionSource(s);
  const host = sessionHost(s);
  const runtime = s.source_instance_id || s.host || "";
  const engine = source === "desktop" ? runtime || host : host && host !== "botmux" ? host : runtime !== "botmux" ? runtime : "";
  const terminal = [
    source === "desktop" || source === "feishu" ? sourceName(source) : "",
    engine && engineName(engine, machine),
    tabLabel(s),
  ].filter(Boolean).join(" · ") || "—";
  const agent = [sessionAgentName(s), modelShort(s.model)].filter(Boolean).join(" · ") || "—";
  const rows = [
    [t("chat-sessioninfo.machine"), machineName || "—"],
    [t("sidebar.nav_terminal"), terminal],
    [t("chat-export.agent"), agent],
    [t("chat-sessioninfo.last_activity"), s.updated_at ? ago(s.updated_at, Date.now(), "narrow") : "—"],
  ];
  return (
    <div data-session-details className="w-72 max-w-full">
      <div className="mb-1.5 flex items-baseline gap-2">
        <span className="min-w-0 flex-1 break-words text-[12px] font-medium text-tooltip-fg">{sessionTitle(s)}</span>
        <span className="shrink-0 text-[11px] text-tooltip-muted">{stateLabel}</span>
      </div>
      <dl className="space-y-0.5">
        {rows.map(([label, value]) => (
          <div key={label} className="grid grid-cols-[76px_minmax(0,1fr)] gap-2 text-[11px] leading-5">
            <dt className="text-tooltip-muted">{label}</dt>
            <dd className="min-w-0 break-words text-tooltip-fg">{value}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}
