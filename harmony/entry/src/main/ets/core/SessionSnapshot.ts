// The launch snapshot of a Hub's session and chat lists (ChatDraftStore `snapshot:<scope>`), drawn before the Hub answers.
// It is one relational-store row, and HarmonyOS's store accepts a value it cannot read back: a value over about 2 MB
// writes but fails to read. A Hub whose session titles held whole pasted prompts (up to ~100 KB each) produced an
// 8 MiB value, so every launch failed to restore it and the Agents list started empty until the Hub answered.
// The snapshot therefore keeps display text short and, past a byte budget, drops the least recently active sessions.

import { utf8Bytes } from './TranscriptCache';

type Payload = Record<string, Object>;

/** Well under the store's ~2 MB readable value, leaving room for a held message written into it later. */
export const SNAPSHOT_BUDGET_BYTES = 1024 * 1024;
/** Longer than any title or preview a list row shows; the Hub's full text replaces it once it answers. */
export const SNAPSHOT_TEXT_LIMIT = 300;

const ACTIVITY_FIELDS: string[] = ['updated_at', 'state_since', 'last_activity', 'seen_at', 'started_at', 'created_at'];

/** `text` cut to `limit` UTF-16 units without splitting a surrogate pair, marked with an ellipsis when cut. */
export function clipText(text: string, limit: number = SNAPSHOT_TEXT_LIMIT): string {
  if (text.length <= limit) return text;
  let end = limit;
  const last = text.charCodeAt(end - 1);
  if (last >= 0xd800 && last <= 0xdbff) end--;
  return text.slice(0, end) + '…';
}

function parsed(raw: Object | undefined): Payload {
  if (typeof raw !== 'string' || !raw) return {};
  try {
    const value = JSON.parse(raw) as Object;
    return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Payload : {};
  } catch (_) { return {}; }
}

/** A copy of a serialized row whose own and raw top-level strings are clipped; nested values (held messages) are kept. */
export function compactRow(row: Payload): Payload {
  const copy: Payload = {};
  Object.keys(row).forEach((key: string) => {
    const value = row[key];
    copy[key] = key !== 'raw' && typeof value === 'string' ? clipText(value) : value;
  });
  if (typeof row['raw'] === 'string') {
    const data = parsed(row['raw']), compact: Payload = {};
    Object.keys(data).forEach((key: string) => {
      const value = data[key];
      compact[key] = typeof value === 'string' ? clipText(value) : value;
    });
    copy['raw'] = JSON.stringify(compact);
  }
  return copy;
}

function activity(row: Payload): number {
  const data = parsed(row['raw']);
  for (const field of ACTIVITY_FIELDS) {
    const value = data[field];
    const at = typeof value === 'string' ? Date.parse(value) : typeof value === 'number' ? value : NaN;
    if (Number.isFinite(at)) return at;
  }
  return 0;
}

/** A row carrying an undelivered message the owner still has to see is never dropped. */
function held(row: Payload): boolean {
  const entries = parsed(row['raw'])['undelivered'];
  return Array.isArray(entries) && entries.length > 0;
}

/**
 * The snapshot's stored text within `budget` UTF-8 bytes: every row compacted, then the least recently active sessions
 * left out (the others keep their order) until it fits. '' when even the rows that must stay do not fit; the caller
 * then stores nothing rather than a value it could not read back.
 */
export function encodeSessionSnapshot(sessions: Payload[], conversations: Payload[], heldActions: Payload[],
  budget: number = SNAPSHOT_BUDGET_BYTES): string {
  const rows = sessions.map(compactRow), chats = conversations.map(compactRow);
  const whole = JSON.stringify({ sessions: rows, conversations: chats, heldActions: heldActions });
  let size = utf8Bytes(whole);
  if (size <= budget) return whole;
  const sizes = rows.map((row: Payload): number => utf8Bytes(JSON.stringify(row)) + 1);
  const order = rows.map((_row: Payload, index: number): number => index).filter((index: number): boolean => !held(rows[index]))
    .sort((a: number, b: number): number => activity(rows[a]) - activity(rows[b]) || b - a);
  const dropped = new Set<number>();
  for (const index of order) {
    if (size <= budget) break;
    dropped.add(index); size -= sizes[index];
  }
  const text = JSON.stringify({ sessions: rows.filter((_row: Payload, index: number): boolean => !dropped.has(index)),
    conversations: chats, heldActions: heldActions });
  return utf8Bytes(text) <= budget ? text : '';
}

/** The stored snapshot, or undefined for an absent, cut or otherwise unreadable one (treated as no snapshot). */
export function decodeSessionSnapshot(text: string): Payload | undefined {
  if (!text) return undefined;
  try {
    const value = JSON.parse(text) as Object;
    return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Payload : undefined;
  } catch (_) { return undefined; }
}
