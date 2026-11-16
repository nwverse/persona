import { useEffect, useRef, useState } from "react";
import { currentLanguage, t } from "../i18n";
import { credentialOwner, errorText } from "./api";
import { shownManager, useLinkManager } from "./manager-choice";

export interface ReplyTranslation {
  text?: string;
  shown: boolean;
  pending: boolean;
  error?: string;
  label: string;
  toggle: () => void;
}

export type ReplyTranslationValue = Partial<Pick<ReplyTranslation, "text" | "shown" | "pending" | "error">>;

/** A paged reply's stable identity and the loader that returns its complete, not merely visible, words. */
export interface ReplyTranslationSource {
  key: string;
  load: () => Promise<string>;
}

export function useReplyTranslation(text: string, source?: ReplyTranslationSource): ReplyTranslation {
  const manager = useLinkManager() ?? shownManager();
  const target = currentLanguage();
  const owner = typeof window === "undefined" ? "" : credentialOwner();
  const key = JSON.stringify([owner, manager, target, source ? [source.key] : text]);
  const current = useRef(key);
  current.current = key;
  const mounted = useRef(false);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);
  const [state, setState] = useState<ReplyTranslationValue & { key: string }>();
  const value = state?.key === key ? state : undefined;
  return {
    ...value,
    shown: Boolean(value?.shown),
    pending: Boolean(value?.pending),
    label: t(value?.pending ? "chat-messageactions.translating" : value?.shown ? "chat-messageactions.show_original" : "chat-messageactions.translate"),
    toggle: () => {
      if (value?.pending) return;
      setState({ ...value, key, pending: true });
      const active = () => mounted.current && current.current === key && credentialOwner() === owner;
      void import("./translate")
        .then(({ nextReplyTranslation }) => nextReplyTranslation(value, source?.load ?? text, target, manager, active))
        .catch((error: unknown) => ({ error: errorText(error) }))
        .then((next) => {
          if (next && active()) setState({ key, ...next });
        });
    },
  };
}
