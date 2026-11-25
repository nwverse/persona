import { MarkdownBlock, parseMarkdown } from './Markdown';

export interface StreamChunkSplit {
  done: string[];
  tail: string;
}

export interface KeyedMarkdownBlock {
  key: string;
  block: MarkdownBlock;
}

class FenceState {
  character: string;
  length: number;
  constructor(character: string, length: number) {
    this.character = character;
    this.length = length;
  }
}

function openFence(trimmed: string): FenceState | undefined {
  if (trimmed.length < 3) return undefined;
  const ch = trimmed[0];
  if (ch !== '`' && ch !== '~') return undefined;
  let count = 0;
  while (count < trimmed.length && trimmed[count] === ch) count++;
  if (count < 3) return undefined;
  const info = trimmed.slice(count);
  if (ch === '`' && info.includes('`')) return undefined;
  return new FenceState(ch, count);
}

function closesFence(trimmed: string, fence: FenceState): boolean {
  if (trimmed.length < fence.length || trimmed[0] !== fence.character) return false;
  let count = 0;
  while (count < trimmed.length && trimmed[count] === fence.character) count++;
  return count >= fence.length && trimmed.slice(count).trim().length === 0;
}

/**
 * Mirrors iOS PersonaKit `StreamChunks.split` (apple/Packages/PersonaKit/Sources/PersonaKit/StreamChunks.swift):
 * splits a streaming reply at definite block boundaries (a blank line outside a code fence followed by an
 * unindented line). Finished chunks stay immutable so only the active tail re-parses during streaming.
 */
export function splitStreamChunks(source: string): StreamChunkSplit {
  const normalized = source.replace(/\r\n?/g, '\n');
  if (!normalized) return { done: [], tail: '' };
  const lines = normalized.split('\n');
  const done: string[] = [];
  let start = 0;
  let fence: FenceState | undefined;
  for (let i = 0; i < lines.length; i++) {
    const trimmed = lines[i].trim();
    if (fence) {
      if (closesFence(trimmed, fence)) fence = undefined;
      continue;
    }
    const opened = openFence(trimmed);
    if (opened) {
      fence = opened;
      continue;
    }
    if (trimmed.length > 0 || i <= start || i + 1 >= lines.length) continue;
    const next = lines[i + 1];
    if (!next || next[0] === ' ' || next[0] === '\t') continue;
    done.push(lines.slice(start, i).join('\n'));
    start = i + 1;
  }
  return { done: done, tail: lines.slice(start).join('\n') };
}

/** Fast deterministic FNV-1a 32-bit digest for stable, JSON.stringify-free block keys. */
export function fastTextHash(value: string): string {
  let hash = 0x811c9dc5;
  for (let i = 0; i < value.length; i++) {
    hash ^= value.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return hash.toString(36) + ':' + String(value.length);
}

class CachedChunk {
  source: string;
  entries: KeyedMarkdownBlock[];
  constructor(source: string, entries: KeyedMarkdownBlock[]) {
    this.source = source;
    this.entries = entries;
  }
}

function keyedBlocks(prefix: string, blocks: MarkdownBlock[]): KeyedMarkdownBlock[] {
  const result: KeyedMarkdownBlock[] = [];
  for (let i = 0; i < blocks.length; i++) {
    const block = blocks[i];
    const body = block.source || block.text || '';
    result.push({ key: prefix + '.' + String(i) + '.' + block.kind + '.' + fastTextHash(body), block: block });
  }
  return result;
}

/**
 * Incremental Markdown block cache for streaming and static messages.
 * Reuses parsed blocks and keys for every unchanged completed chunk (`done[i]`) and parses only the active tail.
 */
export class IncrementalMarkdownCache {
  private lastText: string = '';
  private doneChunks: CachedChunk[] = [];
  private tailSource: string = '';
  private tailEntries: KeyedMarkdownBlock[] = [];
  private flattened: KeyedMarkdownBlock[] = [];
  private parseCountValue: number = 0;

  get parseCount(): number { return this.parseCountValue; }

  update(text: string): KeyedMarkdownBlock[] {
    if (text === this.lastText && (this.flattened.length > 0 || !text)) return this.flattened;
    this.lastText = text;
    if (!text) {
      this.doneChunks = [];
      this.tailSource = '';
      this.tailEntries = [];
      this.flattened = [];
      return this.flattened;
    }
    const split = splitStreamChunks(text);
    const nextDone: CachedChunk[] = [];
    for (let i = 0; i < split.done.length; i++) {
      const chunk = split.done[i];
      const existing = i < this.doneChunks.length ? this.doneChunks[i] : undefined;
      if (existing && existing.source === chunk) {
        nextDone.push(existing);
      } else {
        this.parseCountValue++;
        const blocks = parseMarkdown(chunk);
        nextDone.push(new CachedChunk(chunk, keyedBlocks('c' + String(i), blocks)));
      }
    }
    this.doneChunks = nextDone;

    if (split.tail !== this.tailSource) {
      this.tailSource = split.tail;
      if (split.tail) {
        this.parseCountValue++;
        this.tailEntries = keyedBlocks('t' + String(nextDone.length), parseMarkdown(split.tail));
      } else {
        this.tailEntries = [];
      }
    }

    const merged: KeyedMarkdownBlock[] = [];
    for (const chunk of this.doneChunks) {
      for (const entry of chunk.entries) merged.push(entry);
    }
    for (const entry of this.tailEntries) merged.push(entry);
    this.flattened = merged;
    return this.flattened;
  }

  reset(): void {
    this.lastText = '';
    this.doneChunks = [];
    this.tailSource = '';
    this.tailEntries = [];
    this.flattened = [];
  }
}
