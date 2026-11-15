import { useRef, useState } from "react";
import { currentLanguage, t } from "../i18n";
import { api, credentialOwner, errorText, onCredentialChange } from "./api";
import { BUILTIN_MANAGER, shownManager, stampedHeaders, useLinkManager } from "./manager-choice";

const translations = new Map<string, Promise<string>>();
const KEPT = 200;

onCredentialChange(() => translations.clear());

/** Only selected replies are sent. Pending and successful requests share a tab-local, owner-scoped cache. */
export function translateReply(text: string, target: string, manager: string): Promise<string> {
  const key = JSON.stringify([credentialOwner(), manager, target, text]);
  const cached = translations.get(key);
  if (cached) {
    translations.delete(key);
    translations.set(key, cached);
    return cached;
  }
  const request = api<{ text: string }>("/translate/message", {
    method: "POST",
    json: { text, target },
    headers: stampedHeaders(manager),
  }).then((reply) => {
    if (!reply.text?.trim()) throw new Error(t("chat-messageactions.translation_failed"));
    return reply.text;
  }).catch((error: unknown) => {
    if (translations.get(key) === request) translations.delete(key);
    throw error;
  });
  translations.set(key, request);
  if (translations.size > KEPT) translations.delete(translations.keys().next().value!);
  return request;
}

export interface ReplyTranslation {
  text?: string;
  shown: boolean;
  pending: boolean;
  error?: string;
  label: string;
  toggle: () => void;
}

export function useReplyTranslation(text: string): ReplyTranslation {
  const manager = useLinkManager() ?? (typeof window === "undefined" ? BUILTIN_MANAGER : shownManager());
  const target = currentLanguage();
  const key = JSON.stringify([typeof window === "undefined" ? "" : credentialOwner(), manager, target, text]);
  const current = useRef(key);
  current.current = key;
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
      void translateReply(text, target, manager).then(
        (translated) => {
          if (current.current === key) setState({ key, text: translated, shown: true, pending: false });
        },
        (error: unknown) => {
          if (current.current === key) setState({ key, shown: false, pending: false, error: errorText(error) });
        },
      );
    },
  };
}
