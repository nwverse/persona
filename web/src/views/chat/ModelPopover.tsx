import { useEffect, useMemo, useRef, useState } from "react";
import { t } from "../../i18n";
import { cx, EASE } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

export interface ModelPreset {
  id: string;
  label: string;
  hint?: string;
  current?: boolean;
}

export interface ModelPopoverProps {
  currentModel?: string;
  launcher?: string;
  disabled?: boolean;
  onSelectModel: (modelId: string) => void;
  onCustomModel: () => void;
  /** @internal Initial open state (used in tests). */
  defaultOpen?: boolean;
}

const CLAUDE_PRESETS: readonly ModelPreset[] = [
  { id: "claude-opus-5-5", label: "claude-opus-5-5", hint: "Opus" },
  { id: "claude-sonnet-5-5", label: "claude-sonnet-5-5", hint: "Sonnet" },
  { id: "claude-haiku-5-5", label: "claude-haiku-5-5", hint: "Haiku" },
];

const CODEX_PRESETS: readonly ModelPreset[] = [
  { id: "gpt-6.2-astra", label: "gpt-6.2-astra", hint: "Astra" },
  { id: "gpt-6-astra", label: "gpt-6-astra", hint: "Astra" },
  { id: "gpt-6.1-sol", label: "gpt-6.1-sol", hint: "Sol" },
];

const GEMINI_PRESETS: readonly ModelPreset[] = [
  { id: "gemini-4-neon-high", label: "gemini-4-neon-high", hint: "Neon" },
  { id: "gemini-4-argon-high", label: "gemini-4-argon-high", hint: "Argon" },
  { id: "gemini-3.8-flash-medium", label: "gemini-3.8-flash-medium", hint: "Flash" },
];

export function shortModelName(model?: string): string {
  const trimmed = model?.trim();
  if (!trimmed) return "";
  return trimmed.split("/").pop() || trimmed;
}

/**
 * Returns quick-switch model presets contextual to the session's launcher / currentModel.
 * When `currentModel` is set, it is always placed first with `current: true` and deduplicated.
 */
export function presetModelsFor(launcher?: string, currentModel?: string): ModelPreset[] {
  const l = (launcher ?? "").toLowerCase();
  const m = (currentModel ?? "").toLowerCase();

  let base: readonly ModelPreset[];
  if (l.includes("codex") || l.includes("openai") || m.includes("gpt-") || m.includes("codex")) {
    base = CODEX_PRESETS;
  } else if (l.includes("gemini") || l.includes("agy") || l.includes("antigravity") || m.includes("gemini")) {
    base = GEMINI_PRESETS;
  } else {
    base = CLAUDE_PRESETS;
  }

  const trimmed = currentModel?.trim();
  if (!trimmed) return base.map((p) => ({ ...p }));

  const short = shortModelName(trimmed);
  const lowerTrimmed = trimmed.toLowerCase();
  const lowerShort = short.toLowerCase();

  const matched = base.find(
    (p) => p.id.toLowerCase() === lowerTrimmed || p.id.toLowerCase() === lowerShort,
  );
  const rest = base
    .filter((p) => p !== matched)
    .map((p) => ({ ...p }));

  return [
    {
      id: trimmed,
      label: short,
      hint: matched?.hint,
      current: true,
    },
    ...rest,
  ];
}

export function ModelPopover({
  currentModel,
  launcher,
  disabled = false,
  onSelectModel,
  onCustomModel,
  defaultOpen = false,
}: ModelPopoverProps) {
  const [open, setOpen] = useState(defaultOpen);
  const [active, setActive] = useState(0);
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);

  const presets = useMemo(() => presetModelsFor(launcher, currentModel), [launcher, currentModel]);
  // Last index is the "输入自定义模型…" action.
  const totalItems = presets.length + 1;
  const safeActive = Math.min(Math.max(active, 0), totalItems - 1);

  useEffect(() => {
    if (disabled && open) setOpen(false);
  }, [disabled, open]);

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (e: PointerEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        setOpen(false);
        triggerRef.current?.focus();
        return;
      }
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setActive((i) => (i + 1) % totalItems);
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setActive((i) => (i - 1 + totalItems) % totalItems);
        return;
      }
      if (e.key === "Enter") {
        e.preventDefault();
        if (safeActive < presets.length) {
          onSelectModel(presets[safeActive].id);
        } else {
          onCustomModel();
        }
        setOpen(false);
      }
    };
    document.addEventListener("pointerdown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open, totalItems, safeActive, presets, onSelectModel, onCustomModel]);

  const chipLabel = shortModelName(currentModel) || t("chat-modelpopover.fallback_label");

  const pickPreset = (id: string) => {
    onSelectModel(id);
    setOpen(false);
  };

  const pickCustom = () => {
    onCustomModel();
    setOpen(false);
  };

  return (
    <div ref={rootRef} className="relative inline-flex shrink-0">
      <button
        ref={triggerRef}
        type="button"
        disabled={disabled}
        aria-haspopup="menu"
        aria-expanded={open}
        title={currentModel ? t("chat-modelpopover.model_title", { model: currentModel }) : t("chat-modelpopover.switch_model")}
        onClick={() => {
          if (disabled) return;
          setActive(0);
          setOpen((v) => !v);
        }}
        className={cx(
          "inline-flex h-5 shrink-0 items-center gap-1 rounded-[5px] bg-hover/80 px-1.5 font-mono text-[11px] font-medium whitespace-nowrap text-ink-2 transition-colors hover:bg-hover-2 hover:text-ink disabled:pointer-events-none disabled:opacity-45",
          open && "bg-hover-2 text-ink",
        )}
      >
        <span className="max-w-[140px] truncate">{chipLabel}</span>
        <Icon name="chevron" size={9} stroke={2.4} className={cx("shrink-0 transition-transform duration-150", open && "rotate-180")} />
      </button>

      {open && (
        <div
          role="menu"
          aria-label={t("chat-modelpopover.switch_model")}
          data-chat-find-skip="true"
          className="absolute bottom-full left-0 z-40 mb-2 w-64 rounded-[12px] border border-line-strong bg-surface/95 p-1.5 shadow-overlay backdrop-blur-md"
          style={{ animation: `pop-in 160ms ${EASE} both`, transformOrigin: "bottom left" }}
        >
          <div className="px-2 pt-1 pb-1.5 text-[10.5px] font-medium tracking-[0.04em] text-ink-3">
            {t("chat-modelpopover.switch_model")}
          </div>
          <div className="flex flex-col gap-0.5">
            {presets.map((preset, idx) => {
              const isActive = idx === safeActive;
              return (
                <button
                  key={preset.id}
                  type="button"
                  role="menuitem"
                  onMouseEnter={() => setActive(idx)}
                  onClick={() => pickPreset(preset.id)}
                  className={cx(
                    "flex w-full items-center justify-between gap-2 rounded-[7px] px-2 py-1.5 text-left transition-colors",
                    isActive ? "bg-hover text-ink" : "text-ink-2 hover:bg-hover/60 hover:text-ink",
                  )}
                >
                  <span className="min-w-0 flex-1 truncate font-mono text-[12px]">{preset.label}</span>
                  <span className="flex shrink-0 items-center gap-1.5">
                    {preset.current ? (
                      <span className="rounded-[4px] bg-accent/15 px-1.5 py-0.5 text-[10px] font-medium text-accent">
                        {t("chat-modelpopover.current")}
                      </span>
                    ) : preset.hint ? (
                      <span className="text-[10.5px] text-ink-3">{preset.hint}</span>
                    ) : null}
                  </span>
                </button>
              );
            })}
          </div>

          <div role="separator" className="mx-1 my-1.5 h-px bg-line" />

          <button
            type="button"
            role="menuitem"
            onMouseEnter={() => setActive(presets.length)}
            onClick={pickCustom}
            className={cx(
              "flex w-full items-center gap-2 rounded-[7px] px-2 py-1.5 text-left text-[12px] transition-colors",
              safeActive === presets.length ? "bg-hover text-ink" : "text-ink-2 hover:bg-hover/60 hover:text-ink",
            )}
          >
            <Icon name="pencil" size={12} className="shrink-0 text-ink-3" />
            <span>{t("chat-modelpopover.custom_model")}</span>
          </button>
        </div>
      )}
    </div>
  );
}
