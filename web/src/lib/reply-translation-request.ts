import { t } from "../i18n";
import { api, credentialOwner, onCredentialChange } from "./api";
import { stampedHeaders } from "./manager-choice";

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
