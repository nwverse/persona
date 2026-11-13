// Asking for a GitHub star in three quiet places (#5761, docs/design/DESIGN_SYSTEM.md "GitHub star prompts"): a pill on
// the Agents page's empty start area, a line in the Agents list once this browser has started enough Agents, and a toast
// when first setup finishes. The rules are spec/fixtures/star-prompt's, which Android replays too (iOS: #5910); the
// state is this browser's and never goes to the hub. Whether the user really starred can't be known: a tap on Star counts.
import { useSyncExternalStore } from "react";
import { t } from "../i18n";
import { toast } from "../ui/atoms";
import type { Readiness } from "./readiness";

export const PERSONA_REPO_URL = "https://github.com/nwverse/persona";

export interface StarPromptState {
  /** Agents this browser started. */
  launches: number;
  /** The launches the card waits for; Later doubles it. */
  threshold: number;
  /** Unix ms before which the card stays away (Later's three days), or null. */
  snoozedUntil: number | null;
  /** Star on GitHub was tapped: every prompt stops. */
  done: boolean;
  pillHidden: boolean;
  /** The onboarding toast was shown. */
  welcomed: boolean;
}

export type StarPromptEvent = "launch" | "later" | "star" | "hidePill" | "onboarded";

export const STAR_PROMPT_START: StarPromptState = { launches: 0, threshold: 20, snoozedUntil: null, done: false, pillHidden: false, welcomed: false };
const WAIT_MS = 3 * 24 * 60 * 60 * 1000;

/** The state after `event` at `now`, and whether it showed the onboarding toast. */
export function starPromptAfter(s: StarPromptState, event: StarPromptEvent, now: number): { state: StarPromptState; toast: boolean } {
  switch (event) {
    case "launch":
      return { state: { ...s, launches: s.launches + 1 }, toast: false };
    case "later": {
      let threshold = Math.max(1, s.threshold);
      do threshold *= 2;
      while (threshold <= s.launches);
      return { state: { ...s, threshold, snoozedUntil: now + WAIT_MS }, toast: false };
    }
    case "star":
      return { state: { ...s, done: true }, toast: false };
    case "hidePill":
      return { state: { ...s, pillHidden: true }, toast: false };
    case "onboarded":
      return { state: { ...s, welcomed: true }, toast: !s.done && !s.welcomed };
  }
}

export const showsStarPill = (s: StarPromptState) => !s.done && !s.pillHidden;

export const showsStarCard = (s: StarPromptState, now: number) =>
  !s.done && s.launches >= s.threshold && (s.snoozedUntil == null || now >= s.snoozedUntil);

/** Setup just finished on a hub that never answered a first message, so a long-time user whose computer went away and
 *  came back is never taken for a new one: setup's steps turned ready, or the first answer came. */
export function setupFinished(prev: Readiness | null | undefined, next: Readiness | null | undefined): boolean {
  if (!prev || !next?.ready || firstAnswered(prev)) return false;
  return !prev.ready || firstAnswered(next);
}

const firstAnswered = (r: Readiness) => r.steps.some((s) => s.id === "first_message" && s.state === "ok");

// ── this browser's state ────────────────────────────────

const KEY = "persona.starPrompt";
const listeners = new Set<() => void>();
let cached: { raw: string | null; state: StarPromptState } | null = null;

export function getStarPrompt(): StarPromptState {
  let raw: string | null = null;
  try {
    raw = localStorage.getItem(KEY);
  } catch {
    // storage unavailable: the starting state
  }
  if (cached?.raw === raw) return cached.state;
  cached = { raw, state: parse(raw) };
  return cached.state;
}

function parse(raw: string | null): StarPromptState {
  if (!raw) return STAR_PROMPT_START;
  try {
    const v = JSON.parse(raw) as Partial<StarPromptState>;
    const count = (n: unknown, d: number) => (typeof n === "number" && Number.isFinite(n) && n >= 0 ? n : d);
    return {
      launches: count(v.launches, 0),
      threshold: count(v.threshold, STAR_PROMPT_START.threshold) || STAR_PROMPT_START.threshold,
      snoozedUntil: typeof v.snoozedUntil === "number" ? v.snoozedUntil : null,
      done: v.done === true,
      pillHidden: v.pillHidden === true,
      welcomed: v.welcomed === true,
    };
  } catch {
    return STAR_PROMPT_START;
  }
}

function apply(event: StarPromptEvent): boolean {
  const { state, toast: shown } = starPromptAfter(getStarPrompt(), event, Date.now());
  try {
    localStorage.setItem(KEY, JSON.stringify(state));
  } catch {
    // storage unavailable: nothing is kept
  }
  listeners.forEach((l) => l());
  return shown;
}

const subscribe = (l: () => void) => {
  listeners.add(l);
  if (typeof window !== "undefined") window.addEventListener("storage", l);
  return () => {
    listeners.delete(l);
    if (typeof window !== "undefined") window.removeEventListener("storage", l);
  };
};

export function useStarPrompt(): StarPromptState {
  return useSyncExternalStore(subscribe, getStarPrompt, () => STAR_PROMPT_START);
}

/** The hub took an Agent this browser started. */
export const starPromptLaunched = () => void apply("launch");
export const starPromptLater = () => void apply("later");
export const hideStarPill = () => void apply("hidePill");
/** Star on GitHub was tapped where a link opens the page itself. */
export const starPromptStarred = () => void apply("star");

/** Star on GitHub from a button: opens the project in a new tab, where the user's GitHub session is. */
export function starOnGitHub(): void {
  apply("star");
  window.open(PERSONA_REPO_URL, "_blank", "noopener,noreferrer");
}

/** A readiness answer landed: the toast, once, when it shows setup just finished. */
export function starPromptReadiness(prev: Readiness | null | undefined, next: Readiness | null | undefined): void {
  if (!setupFinished(prev, next) || !apply("onboarded")) return;
  toast(t("lib-star-prompt.welcome"), "info", { label: t("lib-star-prompt.star"), run: starOnGitHub, sticky: true });
}
