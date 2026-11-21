import { t } from "../i18n";
import { setBotmuxMode, type BotmuxMode } from "../lib/botmux-mode";
import { Icon } from "../ui/icons";

const states = {
  decode: { next: "all", action: "chat-botmux.show_raw", icon: "check" },
  all: { next: "hide", action: "chat-botmux.hide", icon: "code" },
  hide: { next: "decode", action: "chat-botmux.decode", icon: "eyeOff" },
} as const;

export default function BotmuxModeChip({ mode }: { mode: BotmuxMode }) {
  const state = states[mode];
  return (
    <button
      type="button"
      title={t(state.action)}
      onClick={() => setBotmuxMode(state.next)}
      data-botmux-mode={mode}
      className="touch-hit relative z-10 flex h-6 items-center gap-1 whitespace-nowrap rounded-full px-[7px] text-[11.5px] text-ink-2 hover:bg-hover hover:text-ink"
    >
      <Icon name={state.icon} size={10} stroke={2.4} />
      {mode === "decode" ? "botmux" : t(mode === "hide" ? "chat-botmux.hidden" : "chat-botmux.raw")}
    </button>
  );
}
