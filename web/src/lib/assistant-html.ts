// The assistant's html component on the web (docs/assistant/assistant-views.md "Free-form HTML" and "Security boundaries every client must verify").
//
// A plain `iframe sandbox="allow-scripts"` with a CSP is not a network-denying sandbox (the frame's own navigation,
// meta refresh and the like are not all stopped by default-src), and a ready timeout cannot stop an endless loop on
// the page's thread. So the web runs a card's code in QuickJS inside a worker it can terminate, and draws what the
// code made into a frame that runs no script (lib/html-runtime/, #4215). Until that runtime has passed the network /
// navigation / loop cases in Chromium and WebKit, WEB_HTML_RUNTIME_VERIFIED stays false and every html card shows its
// plain-text fallback.
//
// What is here: whether to run, the bridge's messages held to the one instance that is on screen, the native
// confirmation with a limit on prompts after a refusal, and the body the hub's html-actions endpoint takes (htmlRequest).
import { TEXT_MOST, openTarget } from "./assistant-blocks";
import type { CardKey } from "./assistant-views";
import type { AssistantHTMLActionInput } from "./extension-wire";

/**
 * Whether the web's html runtime passed the security cases (web/e2e/html-runtime-security.mjs on Chromium and WebKit).
 * False until then: nothing starts it, whatever the hub's assistant.html_views says. Dev pages turn it on for
 * themselves (ui/AssistantCard.tsx HtmlRuntime).
 */
export const WEB_HTML_RUNTIME_VERIFIED = false;

/** The hub's hello feature for the bridge: …/html-actions is there, with persona_view and html_views both on. */
export const HTML_BRIDGE_FEATURE = "assistant_html_bridge";

export type HtmlRunDecision = { run: true } | { run: false; reason: "runtime_unverified" | "disabled" | "no_key" };

/** Whether an html component would run: the hub allows it (hello feature), the component has a stable key, and this
 *  client's runtime passed. The last is false on the web, so this always says fallback here. */
export function htmlDecision(o: { hubAllows: boolean; componentKey?: string; verified?: boolean }): HtmlRunDecision {
  if (!(o.verified ?? WEB_HTML_RUNTIME_VERIFIED)) return { run: false, reason: "runtime_unverified" };
  if (!o.hubAllows) return { run: false, reason: "disabled" };
  if (!o.componentKey) return { run: false, reason: "no_key" };
  return { run: true };
}

/** Whether a view (its root or any named page) has an html component. */
export function hasHtml(raw: unknown): boolean {
  const pages = (v: unknown): unknown[] => {
    if (!v || typeof v !== "object") return [];
    const p = (v as { pages?: unknown }).pages;
    return [v, ...(p && typeof p === "object" ? Object.values(p) : [])];
  };
  return pages(raw).some((page) => {
    const sections = (page as { sections?: unknown }).sections;
    return Array.isArray(sections) && sections.some((s) => Array.isArray(s?.items) && s.items.some((it: unknown) => (it as { type?: unknown })?.type === "html"));
  });
}

// ── the bridge ──

/** What POST …/views/{key}/html-actions takes: the generated AssistantHTMLActionInput, only the two kinds the bridge
 *  has and always confirmed (the user said yes natively, outside the HTML). */
export type HtmlActionRequest = AssistantHTMLActionInput & { kind: "reply" | "open"; confirmed: true };

/** One running html component: a fresh nonce each time it is mounted; its messages carry it. */
export interface HtmlInstance {
  nonce: string;
  conversation: string;
  key: CardKey;
  componentKey: string;
  page: string;
  version: number;
}

export type BridgeAsk = { kind: "reply"; text: string } | { kind: "open"; url: string };

export type BridgeVerdict = { ok: true; ask: BridgeAsk } | { ok: false; reason: "stale" | "shape" | "too_long" | "open" | "quiet" };

/** After a refusal, no new prompt for this long; and at most this many prompts per instance. */
export const REFUSED_QUIET_MS = 10_000;
export const PROMPTS_MOST = 3;

export interface BridgeState {
  /** The instance on screen now (one per card); a message from any other is old and dropped. */
  current?: HtmlInstance;
  prompts: number;
  quietUntil: number;
}

export const newBridge = (): BridgeState => ({ prompts: 0, quietUntil: 0 });

/** Mounting an instance: the older one's messages no longer count, the prompt count starts over. */
export function mounted(_st: BridgeState, inst: HtmlInstance): BridgeState {
  return { current: inst, prompts: 0, quietUntil: 0 };
}

/** Gone from the screen: its messages are old from now on. */
export function unmounted(st: BridgeState, nonce: string): BridgeState {
  return st.current?.nonce === nonce ? { ...st, current: undefined } : st;
}

/**
 * What a message from a frame asks, if it may be put to the user: from the instance on screen (its nonce, the card's
 * version it was started at), one of reply or open in the bridge's shape, within the limits, not while quiet after a
 * refusal or past the prompt limit.
 */
export function judge(st: BridgeState, msg: unknown, version: number, now: number): BridgeVerdict {
  const cur = st.current;
  if (!cur || !msg || typeof msg !== "object") return { ok: false, reason: "stale" };
  const m = msg as Record<string, unknown>;
  if (m.nonce !== cur.nonce || cur.version !== version) return { ok: false, reason: "stale" };
  if (now < st.quietUntil || st.prompts >= PROMPTS_MOST) return { ok: false, reason: "quiet" };
  if (m.kind === "reply" && typeof m.text === "string") {
    if (!m.text.trim()) return { ok: false, reason: "shape" };
    if ([...m.text].length > TEXT_MOST) return { ok: false, reason: "too_long" };
    return { ok: true, ask: { kind: "reply", text: m.text } };
  }
  if (m.kind === "open" && typeof m.url === "string") return openTarget(m.url) ? { ok: true, ask: { kind: "open", url: m.url } } : { ok: false, reason: "open" };
  return { ok: false, reason: "shape" };
}

/** A prompt shown: counted; refused: quiet for a while. */
export function prompted(st: BridgeState, refused: boolean, now: number): BridgeState {
  return { ...st, prompts: st.prompts + 1, quietUntil: refused ? now + REFUSED_QUIET_MS : st.quietUntil };
}

/** The request for what the user confirmed, at the version the instance was started at. */
export function htmlRequest(inst: HtmlInstance, ask: BridgeAsk, clientMsgId: string): HtmlActionRequest {
  return {
    kind: ask.kind,
    component_key: inst.componentKey,
    ...(inst.page ? { page: inst.page } : {}),
    expected_version: inst.version,
    ...(ask.kind === "reply" ? { text: ask.text } : { url: ask.url }),
    client_msg_id: clientMsgId,
    confirmed: true,
  };
}
