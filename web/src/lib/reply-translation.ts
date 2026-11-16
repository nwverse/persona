import { useEffect, useRef, useState } from "react";
import { currentLanguage, t } from "../i18n";
import { credentialOwner, errorText } from "./api";
import { BUILTIN_MANAGER, shownManager, useLinkManager } from "./manager-choice";

export interface ReplyTranslation {
  text?: string;
  shown: boolean;
  pending: boolean;
  error?: string;
  label: string;
  toggle: () => void;
}

/** A paged reply's stable identity and the loader that returns its complete, not merely visible, words. */
export interface ReplyTranslationSource {
  key: string;
  load: () => Promise<string>;
}

export function useReplyTranslation(text: string, source?: ReplyTranslationSource): ReplyTranslation {
  const manager = useLinkManager() ?? (typeof window === "undefined" ? BUILTIN_MANAGER : shownManager());
  const target = currentLanguage();
  const owner = typeof window === "undefined" ? "" : credentialOwner();
  const key = JSON.stringify([owner, manager, target, Boolean(source), source?.key ?? text]);
  const current = useRef(key);
  current.current = key;
  const mounted = useRef(false);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);
  const [state, setState] = useState<{ key: string; text?: string; shown: boolean; pending: boolean; error?: string }>();
  const value = state?.key === key ? state : undefined;
  return {
    ...value,
    shown: Boolean(value?.shown),
    pending: Boolean(value?.pending),
    label: t(value?.pending ? "chat-messageactions.translating" : value?.shown ? "chat-messageactions.show_original" : "chat-messageactions.translate"),
    toggle: () => {
      if (value?.pending) return;
      if (value?.text) {
        setState({ ...value, shown: !value.shown });
        return;
      }
      setState({ key, shown: false, pending: true });
      const active = () => mounted.current && current.current === key && credentialOwner() === owner;
      const translate = async () => {
        const complete = source ? await source.load() : text;
        const { translateReply } = await import("./reply-translation-request");
        // Both loads can outlive this answer or owner; neither may start a stale model request.
        if (!active()) return;
        const translated = await translateReply(complete, target, manager);
        if (active()) setState({ key, text: translated, shown: true, pending: false });
      };
      void translate().catch((error: unknown) => {
        if (active()) setState({ key, shown: false, pending: false, error: errorText(error) });
      });
    },
  };
}
