/**
 * Pure TypeScript helpers for the unified composer-top queue & status strip (#16247).
 * Zero ArkUI dependencies so Node test runners can import directly.
 */

export interface QueuedComposerItem {
  readonly id: string;
  readonly text: string;
  readonly relayed: boolean;
  readonly sender: string;
}

export interface ComposerQueueState {
  readonly items: QueuedComposerItem[];
  readonly ownCount: number;
  readonly relayedCount: number;
  readonly preview: string;
  readonly steersQueue: boolean;
}

function asRecord(value: Object | undefined | null): Record<string, Object> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {};
  return value as Record<string, Object>;
}

function normalizeSingleLine(text: string, maxLength: number = 140): string {
  const collapsed = text.replace(/\s+/g, ' ').trim();
  if (!collapsed) return '';
  if (collapsed.length <= maxLength) return collapsed;
  return collapsed.slice(0, maxLength - 1) + '…';
}

function parseNonNegativeInt(value: Object | undefined): number | undefined {
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) return undefined;
  return Math.floor(value);
}

/**
 * Extracts queued follow-up notes and queue-steering state from a raw session/conversation record.
 * Matches iOS `ComposerTopSlot.swift` (`queued`, `own_queued_count`, `steers_queue`, `assistant && can_send`).
 */
export function extractComposerQueue(sessionData: Record<string, Object> | undefined | null): ComposerQueueState {
  const data = asRecord(sessionData);
  const rawQueued = data['queued'];
  const ownQueuedCount = parseNonNegativeInt(data['own_queued_count']);
  const items: QueuedComposerItem[] = [];

  if (Array.isArray(rawQueued)) {
    const entries = rawQueued as Object[];
    for (let i = 0; i < entries.length; i++) {
      const entry = entries[i];
      if (typeof entry === 'string') {
        const text = entry.trim();
        if (!text) continue;
        const relayed = ownQueuedCount !== undefined ? items.length >= ownQueuedCount : false;
        items.push({
          id: 'queue-' + String(items.length),
          text: text,
          relayed: relayed,
          sender: ''
        });
      } else if (entry && typeof entry === 'object' && !Array.isArray(entry)) {
        const record = entry as Record<string, Object>;
        const rawText = typeof record['text'] === 'string'
          ? record['text'] as string
          : typeof record['body'] === 'string'
            ? record['body'] as string
            : '';
        const text = rawText.trim();
        if (!text) continue;
        const rawId = typeof record['id'] === 'string' ? (record['id'] as string).trim() : '';
        const sender = typeof record['sender'] === 'string' ? (record['sender'] as string).trim() : '';
        const explicitRelayed = typeof record['relayed'] === 'boolean' ? record['relayed'] as boolean : undefined;
        const relayed = explicitRelayed !== undefined
          ? explicitRelayed
          : ownQueuedCount !== undefined
            ? items.length >= ownQueuedCount
            : !!sender;
        items.push({
          id: rawId || 'queue-' + String(items.length),
          text: text,
          relayed: relayed,
          sender: sender
        });
      }
    }
  }

  let relayedCount = 0;
  for (let i = 0; i < items.length; i++) {
    if (items[i].relayed) relayedCount++;
  }
  const ownCount = items.length - relayedCount;

  const firstOwn = items.find((item) => !item.relayed);
  const previewSource = firstOwn ? firstOwn.text : (items.length > 0 ? items[0].text : '');
  const preview = normalizeSingleLine(previewSource);

  const steersQueue = data['steers_queue'] === true ||
    (data['assistant'] === true && data['can_send'] === true);

  return {
    items: items,
    ownCount: ownCount,
    relayedCount: relayedCount,
    preview: preview,
    steersQueue: steersQueue
  };
}

/**
 * Formats an active turn's elapsed duration concisely:
 * `0s..59s` -> `Ns`, `>=60s` -> `Mm SSs`.
 */
export function formatTurnElapsed(sinceMs: number, nowMs: number): string {
  if (!Number.isFinite(sinceMs) || !Number.isFinite(nowMs) || sinceMs <= 0) return '0s';
  const totalSeconds = Math.max(0, Math.floor((nowMs - sinceMs) / 1000));
  if (totalSeconds < 60) return String(totalSeconds) + 's';
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return String(minutes) + 'm ' + String(seconds).padStart(2, '0') + 's';
}
