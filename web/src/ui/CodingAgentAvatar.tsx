// Contact-style coding Agent avatar with a compact circular outlined hosting-engine corner badge.
// Keeps the main model/vendor mark centered, places the hosting engine logo at the bottom-right corner, and reserves
// the top-right corner for live status or remote-host indicators so none overlap.
import type { Machine, StatusMarkState } from "../lib/types";
import {
  codingAgentAvatarLabel,
  resolveCodingAgentAvatar,
  resolveHostingEngine,
  type CodingAgentAvatarSession,
  type HostingEngineBadgeInfo,
  type KnownHostingEngine,
} from "../lib/coding-agent-avatar";
import { AgentMark, RemoteSignal } from "./AgentMark";
import { StatusMark } from "./StatusMark";
import { cx } from "./atoms";
import orcaBadge from "./engines/orca.svg?no-inline";
import herdrBadge from "./engines/herdr.svg?no-inline";
import herdrCoreBadge from "./engines/herdr-core.svg?no-inline";
import paseoBadge from "./engines/paseo.svg?no-inline";
import t3codeBadge from "./engines/t3code.svg?no-inline";
import cindyBadge from "./engines/cindy.svg?no-inline";
import ternBadge from "./engines/tern.svg?no-inline";
import rexBadge from "./engines/rex.svg?no-inline";
import botmuxBadge from "./engines/botmux.svg?no-inline";

export interface CodingAgentAvatarStatus {
  state: StatusMarkState;
  awaiting?: boolean;
  finished?: boolean;
  waiting?: boolean;
}

export type CodingAgentAvatarRing = "page" | "canvas" | "surface";

const RING: Record<CodingAgentAvatarRing, [ring: string, bg: string]> = {
  page: ["ring-page", "bg-page"],
  canvas: ["ring-canvas", "bg-canvas"],
  surface: ["ring-surface", "bg-surface"],
};

// External ?no-inline SVG badges match AgentMark's asset pattern and keep FIRST_SCREEN_BUDGET flat.
const BADGES: Record<KnownHostingEngine, string> = {
  orca: orcaBadge,
  herdr: herdrBadge,
  "herdr-core": herdrCoreBadge,
  paseo: paseoBadge,
  t3code: t3codeBadge,
  cindy: cindyBadge,
  tern: ternBadge,
  rex: rexBadge,
  botmux: botmuxBadge,
};

export interface CodingAgentAvatarProps {
  session: CodingAgentAvatarSession;
  size?: number;
  markSize?: number;
  ring?: CodingAgentAvatarRing;
  status?: CodingAgentAvatarStatus;
  remote?: boolean;
  showEngineBadge?: boolean;
  machine?: Pick<Machine, "engines">;
  decorative?: boolean;
  className?: string;
}

export function CodingAgentAvatar({
  session,
  size = 40,
  markSize,
  ring = "page",
  status,
  remote,
  showEngineBadge = true,
  machine,
  decorative,
  className,
}: CodingAgentAvatarProps) {
  const avatar = resolveCodingAgentAvatar(session);
  const engine = showEngineBadge ? resolveHostingEngine(session, machine) : null;
  const label = codingAgentAvatarLabel(avatar, engine);
  const inner = markSize ?? Math.max(12, Math.round(size * 0.56));
  const badge = Math.max(11, Math.min(20, Math.round(size * 0.42)));
  const stBox = size >= 36 ? 14 : 12;
  const [ringCls, bgCls] = RING[ring];
  const a11y = decorative ? { "aria-hidden": true as const } : { role: "img" as const, "aria-label": label, title: label };

  return (
    <span
      {...a11y}
      data-coding-agent-avatar={avatar.mark ?? "letter"}
      data-hosting-engine={engine?.id}
      className={cx("relative inline-flex shrink-0 items-center justify-center select-none text-ink-3", className)}
      style={{ width: size, height: size }}
    >
      <span className="flex size-full items-center justify-center overflow-hidden rounded-full bg-surface text-ink shadow-hairline ring-1 ring-line">
        {avatar.mark ? (
          <AgentMark agent={avatar.mark} size={inner} decorative className="text-ink" />
        ) : (
          <span aria-hidden data-agent-mark="letter" className="font-semibold leading-none text-ink-2" style={{ fontSize: Math.max(9, Math.round(inner * 0.68)) }}>
            {avatar.letter}
          </span>
        )}
      </span>

      {status && (
        <span
          data-avatar-status={status.state}
          className={cx("absolute -top-0.5 -right-0.5 z-10 flex items-center justify-center rounded-full", bgCls, size >= 32 ? "ring-2" : "ring-1", ringCls)}
          style={{ width: stBox, height: stBox }}
        >
          <StatusMark state={status.state} size="sm" awaiting={status.awaiting} finished={status.finished} waiting={status.waiting} />
        </span>
      )}

      {remote && !status && <RemoteSignal size={size} />}

      {engine && <HostingEngineBadge engine={engine} size={badge} ring={ring} decorative={decorative} className="absolute -right-0.5 -bottom-0.5 z-10" />}
    </span>
  );
}

export function HostingEngineBadge({
  engine,
  size = 16,
  ring = "page",
  decorative = true,
  className,
}: {
  engine: HostingEngineBadgeInfo;
  size?: number;
  ring?: CodingAgentAvatarRing;
  decorative?: boolean;
  className?: string;
}) {
  const src = engine.kind === "custom" ? undefined : BADGES[engine.kind];
  const a11y = decorative ? { "aria-hidden": true as const } : { role: "img" as const, "aria-label": engine.name, title: engine.name };

  return (
    <span
      {...a11y}
      data-engine-badge={engine.id}
      data-engine-kind={engine.kind}
      className={cx("inline-flex shrink-0 items-center justify-center overflow-hidden rounded-full text-white", size >= 15 ? "ring-2" : "ring-1", RING[ring][0], className)}
      style={{ width: size, height: size, backgroundColor: src ? undefined : "#52525b" }}
    >
      {src ? (
        <img src={src} alt="" aria-hidden width={size} height={size} className="block size-full" />
      ) : (
        <span aria-hidden className="font-bold leading-none" style={{ fontSize: Math.max(7, Math.round(size * 0.58)) }}>{engine.letter}</span>
      )}
    </span>
  );
}
