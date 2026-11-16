import type { ParkContact, ParkEvidence, TaskReview } from "./supervision";
import type { Todo } from "./todos";

/** Rendering a source never turns an unchecked URL into an executable link. */
export function waitSource(raw?: string): string | undefined {
  if (!raw) return undefined;
  try {
    const url = new URL(raw);
    if (!["https:", "http:"].includes(url.protocol) || url.username || url.password || !url.hostname || url.pathname === "/") return undefined;
    return url.href;
  } catch {
    return undefined;
  }
}

function past(raw: string | undefined, now: number): number | undefined {
  const ms = raw ? Date.parse(raw) : NaN;
  return Number.isFinite(ms) && ms > 0 && ms <= now ? ms : undefined;
}

export function sentWaitContact(evidence: ParkEvidence | undefined, now: number): ParkContact | undefined {
  if (evidence?.kind !== "person_reply" && evidence?.kind !== "merge_review") return undefined;
  if (evidence.kind === "merge_review") {
    const object = waitSource(evidence.url);
    if (evidence.ready !== true || !object || !/(?:\/pull\/|\/-\/merge_requests\/)[1-9]\d*\/?$/.test(new URL(object).pathname)) return undefined;
  }
  const c = evidence?.contact;
  return c?.status === "sent" && c.channel?.trim() && c.sender?.trim() && c.request?.trim() &&
    waitSource(c.url) && past(c.sent_at, now) !== undefined ? c : undefined;
}

function identity(evidence: ParkEvidence | undefined, waitingOn?: string, reason?: string): string {
  // A reminder's receipt URL and recorder can change without starting a different external wait.
  return evidence ? JSON.stringify([evidence.kind, evidence.target?.trim(), evidence.kind === "person_reply" ? evidence.contact?.channel : waitSource(evidence.url)]) :
    JSON.stringify(["legacy", waitingOn, reason]);
}

/** Earliest known start of this uninterrupted wait, not the task's update or a PR poll. */
export function waitStart(todo: Todo, now: number): { at?: number; request: boolean } {
  const sup = todo.supervision;
  const p = sup?.parked;
  if (todo.done_at || sup?.state !== "parked" || !p) return { request: false };
  const contact = sentWaitContact(p.evidence, now);
  let at = contact ? past(contact.sent_at, now) : undefined;
  let request = !!contact;
  const reviews = sup.reviews ?? [];
  const key = identity(p.evidence, p.waiting_on, p.reason);
  const count = Math.max(1, p.count || 1);
  for (let i = reviews.length - 1, n = 0; i >= 0 && n < count; i--, n++) {
    const r: TaskReview = reviews[i];
    if (r.verdict !== "parked" || identity(r.evidence, r.waiting_on ?? p.waiting_on, r.reason ?? p.reason) !== key) break;
    // An Agent turn resets parks even when no intervening review was appended.
    if (n && r.parks !== undefined && r.parks !== count - n) break;
    const c = sentWaitContact(r.evidence, now);
    const start = c ? past(c.sent_at, now) : past(r.at, now);
    if (start !== undefined && (at === undefined || start < at)) {
      at = start;
      request = !!c;
    }
    if (r.parks === 1) break;
  }
  return { at, request };
}

/** Known starts first, oldest first; unknowns last. Identity ties never depend on a mutable last update. */
export function compareWaitAge(a: Todo, b: Todo, now: number): number {
  return (waitStart(a, now).at ?? Infinity) - (waitStart(b, now).at ?? Infinity) || a.id.localeCompare(b.id);
}

