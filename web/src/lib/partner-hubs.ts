// Settings › This hub › Partner hubs (docs/server/partner-hubs.md): the independent hubs this one is paired with, whose
// assistants send each other messages. The pairing and the links are the hub's (/api/v1/partner-hubs); "Ask me before
// sending" is the default manager's (/api/v1/partner-messages/prefs, no manager header: only the default sends). The hub
// lists the `partner_hubs` feature when it can pair and pushes {"type":"partner_hubs"} when the list or a link changes.
import { t } from "../i18n";
import { api, ApiError, enc, errorText } from "./api";
import { ago } from "./format";

export const PARTNER_HUBS_FEATURE = "partner_hubs";
/** The WebSocket push that says the list changed: read it again. */
export const PARTNER_HUBS_EVENT = "partner_hubs";

export type PartnerState = "connected" | "connecting" | "offline" | "refused";
export type PartnerDial = "auto" | "never";

/** One paired hub as the hub reports it (PartnerHubView). */
export interface PartnerHub {
  hub_id: string;
  name: string;
  /** Its TLS public key fingerprint, as its pairing code carried it. */
  fp: string;
  state: PartnerState | (string & {});
  /** Whether this hub dials it: "never" waits for it to connect here (it cannot be reached from this hub). */
  dial: PartnerDial;
  /** When the current link came up (connected only). */
  since?: string;
  last_seen?: string;
  /** The last dial's failure, in the interface language. */
  error?: string;
  dialed_by?: "us" | "them" | "";
  rtt_ms?: number;
  created_at: string;
}

export interface PartnerHubList {
  /** Partner hubs are on here (hub.json partners.enabled). */
  enabled: boolean;
  /** This hub is a linked-hubs (mesh) member: it cannot pair. */
  member: boolean;
  /** It can show a code: other hubs can reach it over TLS. */
  can_issue: boolean;
  hubs: PartnerHub[];
}

export interface PartnerCode {
  code: string;
  expires_at: string;
}

/** Whether the assistant's messages to that hub wait for the owner's OK. A hub with no stored row asks (true). */
export interface PartnerPref {
  hub_id: string;
  confirm: boolean;
}

export const listPartnerHubs = () => api<PartnerHubList>("/partner-hubs");
export const makePartnerCode = () => api<PartnerCode>("/partner-hubs/code", { method: "POST" });
// A 401 here (code_refused) is the other hub refusing the code (used or expired), not this page's credential: it must
// not sign the page out. A credential that really stopped working still does on the next request.
export const pairPartnerHub = (code: string) => api<PartnerHub>("/partner-hubs", { method: "POST", json: { code }, isAuthContextCurrent: () => false });
export const setPartnerDial = (id: string, dial: PartnerDial) => api<PartnerHub>(`/partner-hubs/${enc(id)}`, { method: "PATCH", json: { dial } });
export const unpairPartnerHub = (id: string) => api<{ ok: boolean }>(`/partner-hubs/${enc(id)}`, { method: "DELETE" });
export const listPartnerPrefs = () => api<{ prefs: PartnerPref[] }>("/partner-messages/prefs");
export const setPartnerConfirm = (id: string, confirm: boolean) => api<PartnerPref>(`/partner-messages/prefs/${enc(id)}`, { method: "PUT", json: { confirm } });

/** Whether messages to `id` wait for the owner's OK: the stored value, true without one; undefined until the prefs are read. */
export function confirmFor(prefs: PartnerPref[] | null, id: string): boolean | undefined {
  if (!prefs) return undefined;
  return prefs.find((p) => p.hub_id === id)?.confirm ?? true;
}

/** prefs with `pref` in place of the hub's old value. */
export function withPref(prefs: PartnerPref[] | null, pref: PartnerPref): PartnerPref[] {
  return [...(prefs ?? []).filter((p) => p.hub_id !== pref.hub_id), pref];
}

/** What a pasted code names, shown before pairing: the hub and the fingerprint to compare with the one its own screen shows. */
export interface PartnerCodeInfo {
  hub_id: string;
  name: string;
  fp: string;
  /** The code as it is sent: the pasted text without the spaces and line breaks a copy may add. */
  code: string;
}

const PAIR_SECRET = /^jh_[0-9a-f]{64}$/i;

/** A persona://partner?… code read, or null when the text is not one (another link, a cut-off copy). */
export function parsePartnerCode(text: string): PartnerCodeInfo | null {
  // the values are URL-encoded, so a space or line break in the text is one a copy put there
  const code = text.replace(/\s+/g, "");
  let url: URL;
  try {
    url = new URL(code);
  } catch {
    return null;
  }
  if (url.protocol !== "persona:" || url.hostname !== "partner") return null;
  const q = url.searchParams;
  const hub = q.get("hub")?.trim() ?? "";
  const fp = q.get("fp")?.trim() ?? "";
  if (!hub || !fp || !PAIR_SECRET.test(q.get("code") ?? "")) return null;
  return { hub_id: hub, name: q.get("name")?.trim() || hub, fp, code };
}

export type PartnerTone = "green" | "accent" | "red";

/** The state's pill: its words and color. */
export function partnerStatus(h: PartnerHub): { label: string; tone?: PartnerTone } {
  switch (h.state) {
    case "connected":
      return { label: t("settings-partnerhubs.state_connected"), tone: "green" };
    case "connecting":
      return { label: t("settings-partnerhubs.state_connecting"), tone: "accent" };
    case "refused":
      return { label: t("settings-partnerhubs.state_refused"), tone: "red" };
    default:
      return { label: t("settings-partnerhubs.state_offline") };
  }
}

/** The line under the name: since when it is connected, or when it last was, or what this hub waits for. */
export function partnerLine(h: PartnerHub, now = Date.now()): string {
  if (h.state === "connected") {
    const since = ago(h.since, now);
    return since ? t("settings-partnerhubs.connected_since", { time: since }) : "";
  }
  const seen = ago(h.last_seen, now);
  if (seen) return t("settings-partnerhubs.last_seen", { time: seen });
  return h.dial === "never" ? t("settings-partnerhubs.waits_for_it") : t("settings-partnerhubs.not_yet");
}

/** The hub's own sentence for a refusal, "" when it gave none. */
function hubWords(e: ApiError): string {
  return e.isJson && e.message !== `HTTP ${e.status}` ? e.message : "";
}

/** A pairing change that failed, in plain words. The page's words only where the code says which hub refused: the
 *  other hub's refusal of a pairing comes back as this hub's 409 with the other hub's code (partners_off, member_hub,
 *  pin_changed…), and the hub's own sentence says whose it is. */
export function partnerError(e: unknown): string {
  if (e instanceof ApiError) {
    switch (e.code) {
      case "partners_off":
        // this hub's is a 403; the other hub's comes as a 409
        return t(e.status === 403 ? "settings-partnerhubs.err_off" : "settings-partnerhubs.err_other_off");
      case "no_tls":
        return t("settings-partnerhubs.err_no_tls");
      case "own_code":
        return t("settings-partnerhubs.err_own_code");
      case "already_linked": // the same on either side: linked hubs are linked both ways
        return t("settings-partnerhubs.err_already_linked");
      case "code_refused":
        return t("settings-partnerhubs.err_code_refused");
      case "member_hub":
        return hubWords(e) || t("settings-partnerhubs.err_member");
      case "pin_changed":
        return hubWords(e) || t("settings-partnerhubs.err_pin_changed");
      case "bad_code": // not a code, incomplete, or without a secure address: the hub says which
        return hubWords(e) || t("settings-partnerhubs.err_bad_code");
      case "unreachable": {
        // the other hub could not be reached, not this one: the plain sentence, with how each address failed
        const detail = hubWords(e);
        return detail ? t("settings-partnerhubs.err_unreachable_detail", { detail }) : t("settings-partnerhubs.err_unreachable");
      }
    }
    // the other hub answered but did not pair (wrong_hub, no_credentials, refused): the hub's own sentence says how
    if (e.status === 502 && hubWords(e)) return hubWords(e);
  }
  // anything else (this page's own credential refused, a write the hub could not save) as every page says it
  return errorText(e);
}
