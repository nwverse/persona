/** A small, dependency-free Markdown tree for chat and resource text. */
export type MarkdownInlineKind = 'text' | 'bold' | 'italic' | 'code' | 'link' | 'file' | 'image';
export type MarkdownBlockKind = 'paragraph' | 'heading' | 'code' | 'rule' | 'quote' | 'list' | 'list-item' | 'table' | 'raw';

// System body, the rounded MarkdownUI 17-point ScaledMetric, and the CJK fallback's measured advance.
// CoreText switches from PingFang UI Text to its optically narrower Display face at 20 points.
const replyTypeSizes: number[][] = [
  [14,15,14.883], [15,15,14.883], [16,16,15.875], [17,17,16.851], [19,19,18.834], [21,20,19.154],
  [23,22,20.945], [28,26,24.754], [33,31,29.427], [40,37,35.018], [47,43,40.656], [53,48,45.293]
];

export function agentReplyBodySize(fontScale: number, han: boolean = false): number {
  const body = Math.min(53, Math.max(14, 17 * fontScale));
  let upper = 1;
  while (upper < replyTypeSizes.length - 1 && body > replyTypeSizes[upper][0]) upper++;
  const a = replyTypeSizes[upper - 1], b = replyTypeSizes[upper], column = han ? 2 : 1;
  return a[column] + (b[column] - a[column]) * (body - a[0]) / (b[0] - a[0]);
}

/** Font advances of the native recap's mixed CJK and Latin body at standard and largest Dynamic Type. */
export function recapGlyphSize(bodyPoints: number, han: boolean): number {
  const regular = han ? 16.4 : 17;
  const largest = han ? 43.6 : 42;
  if (bodyPoints <= 17) return bodyPoints * regular / 17;
  return regular + (bodyPoints - 17) * (largest - regular) / (48 - 17);
}

export class MarkdownProseRun {
  text: string;
  han: boolean;
  constructor(text: string, han: boolean) { this.text = text; this.han = han; }
}

export function markdownProseRuns(text: string): MarkdownProseRun[] {
  const result: MarkdownProseRun[] = [];
  for (const scalar of text) {
    const point = scalar.codePointAt(0) ?? 0;
    const han = point >= 0x3400 && point <= 0x9fff || point >= 0xf900 && point <= 0xfaff ||
      point >= 0x20000 && point <= 0x323af || point >= 0x3000 && point <= 0x303f || point >= 0xff01 && point <= 0xff60;
    const previous = result[result.length - 1];
    const variation = point >= 0xfe00 && point <= 0xfe0f || point >= 0xe0100 && point <= 0xe01ef;
    if (previous && (previous.han === han || variation)) previous.text += scalar;
    else result.push(new MarkdownProseRun(scalar,han));
  }
  return result;
}

export function hasHanProse(nodes: MarkdownInline[]): boolean {
  return nodes.some((node: MarkdownInline) => node.kind === 'text' && /[\u3400-\u9fff]/.test(node.text) ||
    (node.kind === 'bold' || node.kind === 'italic') && hasHanProse(node.children));
}

export class MarkdownInline {
  kind: MarkdownInlineKind = 'text';
  text: string = '';
  url: string = '';
  source: string = '';
  children: MarkdownInline[] = [];
  /** Inline code (or a file label drawn as code) that meets a CJK letter directly gets a thin space on that side. */
  gapBefore: boolean = false;
  gapAfter: boolean = false;

  constructor(kind: MarkdownInlineKind = 'text', text: string = '', url: string = '', children: MarkdownInline[] = [], source: string = '') {
    this.kind = kind;
    this.text = text;
    this.url = url;
    this.children = children;
    this.source = source;
  }
}

export class MarkdownTableRow {
  cells: MarkdownInline[][] = [];
  header: boolean = false;
}

export class MarkdownBlock {
  kind: MarkdownBlockKind = 'paragraph';
  source: string = '';
  text: string = '';
  language: string = '';
  marker: string = '';
  ordered: boolean = false;
  level: number = 0;
  children: MarkdownBlock[] = [];
  items: MarkdownBlock[] = [];
  rows: MarkdownTableRow[] = [];
  alignments: string[] = [];
  inlines: MarkdownInline[] = [];

  constructor(kind: MarkdownBlockKind = 'paragraph', source: string = '') {
    this.kind = kind;
    this.source = source;
  }
}

class ListMarker {
  indent: number = 0;
  marker: string = '';
  content: string = '';
  contentIndent: number = 0;
  ordered: boolean = false;
}

class FenceMarker {
  character: string = '`';
  length: number = 3;
  language: string = '';
}

class LinkCandidate {
  end: number = 0;
  source: string = '';
  label: string = '';
  url: string = '';
}

function listMarker(line: string): ListMarker | undefined {
  const match = /^( *)([-+*]|[0-9]{1,9}[.)])(?:([ \t]+)(.*)|([ \t]*))$/.exec(line);
  if (!match || match[1].length > 3) return undefined;
  const marker = new ListMarker();
  marker.indent = match[1].length;
  marker.marker = match[2];
  marker.content = match[4] || '';
  const spacing = match[3] || ' ';
  marker.contentIndent = marker.indent + marker.marker.length + spacing.length;
  marker.ordered = /^[0-9]/.test(marker.marker);
  return marker;
}

function fenceMarker(line: string): FenceMarker | undefined {
  const match = /^ {0,3}(`{3,}|~{3,})(.*)$/.exec(line);
  if (!match) return undefined;
  const character = match[1][0];
  const info = match[2].trim();
  if (character === '`' && info.includes('`')) return undefined;
  const marker = new FenceMarker();
  marker.character = character;
  marker.length = match[1].length;
  marker.language = info.split(/[ \t]+/)[0] || '';
  return marker;
}

function isFenceClose(line: string, fence: FenceMarker): boolean {
  const body = line.replace(/^ {0,3}/, '');
  let count = 0;
  while (count < body.length && body[count] === fence.character) count++;
  return count >= fence.length && body.slice(count).trim().length === 0;
}

function headingMatch(line: string): RegExpExecArray | null {
  return /^( {0,3})(#{1,6})(?:[ \t]+|$)(.*)$/.exec(line);
}

function headingBody(value: string): string {
  return value.replace(/[ \t]+#+[ \t]*$/, '').trim();
}

function isRule(line: string): boolean {
  return /^( {0,3})(?:\*[ \t]*){3,}$/.test(line) || /^( {0,3})(?:-[ \t]*){3,}$/.test(line) || /^( {0,3})(?:_[ \t]*){3,}$/.test(line);
}

function quotePrefix(line: string): RegExpExecArray | null {
  return /^ {0,3}>[ \t]?/.exec(line);
}

function splitTableCells(line: string): string[] {
  let value = line.trim();
  if (value.startsWith('|')) value = value.slice(1);
  if (value.endsWith('|') && !endsWithEscapedPipe(value)) value = value.slice(0, -1);
  const cells: string[] = [];
  let cell = '';
  let codeTicks = 0;
  for (let index = 0; index < value.length;) {
    const character = value[index];
    if (character === '\\' && index + 1 < value.length) {
      cell += codeTicks && value[index + 1] === '|' ? '|' : character + value[index + 1];
      index += 2;
      continue;
    }
    if (character === '`') {
      let end = index + 1;
      while (end < value.length && value[end] === '`') end++;
      const count = end - index;
      if (codeTicks === 0) codeTicks = count;
      else if (codeTicks === count) codeTicks = 0;
      cell += value.slice(index, end);
      index = end;
      continue;
    }
    if (character === '<' && codeTicks === 0) {
      const br = /^<br\s*\/?>/i.exec(value.slice(index));
      if (br) { cell += '\n'; index += br[0].length; continue; }
    }
    if (character === '|' && codeTicks === 0) {
      cells.push(cell.trim());
      cell = '';
    } else cell += character;
    index++;
  }
  cells.push(cell.trim());
  return cells;
}

function endsWithEscapedPipe(value: string): boolean {
  if (!value.endsWith('|')) return false;
  let slashes = 0;
  for (let index = value.length - 2; index >= 0 && value[index] === '\\'; index--) slashes++;
  return slashes % 2 === 1;
}

function hasTableSeparator(line: string): boolean {
  const trimmed = line.trim();
  return !/^ {4}/.test(line) && (splitTableCells(line).length > 1 || trimmed.startsWith('|') ||
    (trimmed.endsWith('|') && !endsWithEscapedPipe(trimmed)));
}

function tableAlignments(line: string, columns: number): string[] | undefined {
  if (/^ {4}/.test(line)) return undefined;
  const cells = splitTableCells(line);
  if (cells.length !== columns || columns < 1) return undefined;
  const result: string[] = [];
  for (const cell of cells) {
    if (!/^:?-{1,}:?$/.test(cell)) return undefined;
    const left = cell.startsWith(':');
    const right = cell.endsWith(':');
    result.push(left && right ? 'center' : right ? 'end' : 'start');
  }
  return result;
}

// File references are presented separately from links that can open a web reader.
export function markdownFilePath(destination: string): string {
  if (!destination || destination.startsWith('//') || /[\u0000-\u001f\u007f\\<>?"%]/.test(destination)) return '';
  const path = destination.replace(/(?::[1-9]\d*(?::[1-9]\d*)?|#L[1-9]\d*(?:-L?[1-9]\d*)?)$/, '');
  if (path.includes(':') || path.includes('#') || path.split('/').includes('..')) return '';
  return /(?:^|\/)[^/]+\.[a-zA-Z0-9][a-zA-Z0-9_+-]{0,15}$/.test(path) ? path : '';
}

export function chatFilePath(destination: string, cwd: string): string {
  const path = markdownFilePath(destination), root = cwd.replace(/\/+$/, '');
  if (!path || !cwd.startsWith('/') || cwd.split('/').includes('..')) return '';
  if (path.startsWith('/')) return path.startsWith(root + '/') ? path : '';
  return root + '/' + path.replace(/^\.\//, '');
}

function localImageDestination(url: string): string {
  if (!/\.(png|jpe?g|gif|webp|heic|heif)$/i.test(url) || /%(?:2f|5c|2e|25)/i.test(url)) return '';
  try { return markdownFilePath(decodeURIComponent(url).replace(/\\([()])/g, '$1')); }
  catch (_) { return ''; }
}

/** Resolve images on the paired machine, including the current user’s temporary exports. */
export function chatImagePath(url: string, cwd: string): string {
  const path = localImageDestination(url);
  if (!path || path.includes('//')) return '';
  const workspacePath = chatFilePath(path, cwd);
  if (workspacePath) return workspacePath;
  const home = /^(\/(?:Users|home)\/[^/]+)(?:\/|$)/.exec(cwd);
  if (home && !cwd.split('/').includes('..') && path.startsWith(home[1] + '/tmp/')) return path;
  return /^\/(?:[^/]+\/)*\.codex(?:-[\w-]+)?\/visualizations\/\d{4}\/\d{2}\/\d{2}\/[\w-]+\/(?:[^/.][^/]*\/)*[^/]+$/.test(path) ? path : '';
}

export type MarkdownLinkProvider = 'github' | 'figma' | 'linear' | 'notion' | 'googledrive';

/** Match the provider by the URL authority, independent of the visible Markdown label. */
export function markdownLinkProvider(url: string): MarkdownLinkProvider | undefined {
  if (!safeHttpUrl(url)) return undefined;
  const match = /^https?:\/\/([a-z0-9.-]+)(?::[0-9]+)?(?:[/?#]|$)/i.exec(url);
  const host = match?.[1].toLowerCase();
  if (!host) return undefined;
  if (host === 'github.com' || host.endsWith('.github.com')) return 'github';
  if (host === 'figma.com' || host.endsWith('.figma.com')) return 'figma';
  if (host === 'linear.app') return 'linear';
  if (host === 'notion.so' || host.endsWith('.notion.so') || host === 'app.notion.com') return 'notion';
  if (['docs.google.com', 'drive.google.com', 'sheets.google.com', 'slides.google.com'].includes(host)) return 'googledrive';
  return undefined;
}

/** Keep the slash with a short command in prose. Leave URLs, paths and inline code literal. */
export function markdownProseText(text: string): string {
  return text.replace(/(^|[\s（(「“])\/(?=[a-zA-Z][\w-]{0,31}(?:$|[\s.,!?;:，。！？、；：）)”」]|[\u3400-\u9fff]))/g, '$1/\u2060');
}

/** Convert native UTF-16 offsets to source text, omitting only marks inserted by the renderer. */
export function markdownCodeSelection(nodes: readonly MarkdownInline[], start: number, end: number, transcriptMarks: boolean = false,
  workspaceRoot: string = '', hasMediaOwner: boolean = false): string | undefined {
  let cursor = 0, selected = '', decoration = false;
  function append(value: string): void {
    selected += value.slice(Math.max(0, start - cursor), Math.max(0, end - cursor));
    cursor += value.length;
  }
  function pad(): void { if (cursor >= start && cursor < end) decoration = true; cursor++; }
  function prose(value: string): void {
    const rendered = transcriptMarks ? markdownProseText(value) : value;
    if (rendered === value) { append(value); return; }
    let original = 0;
    for (let i = 0; i < rendered.length; i++) {
      if (rendered[i] === value[original]) append(value[original++]);
      else pad();
    }
  }
  function code(node: MarkdownInline, value: string): void {
    if (node.gapBefore) pad();
    append(value);
    if (node.gapAfter) pad();
  }
  function read(parts: readonly MarkdownInline[]): void {
    for (const node of parts) {
      if (node.kind === 'code') code(node, node.text);
      else if (node.kind === 'text') prose(node.text);
      else if (node.kind === 'image') prose(markdownInlineText(node.children));
      else if (node.kind === 'file') {
        const label = markdownInlineText(node.children);
        if (hasMediaOwner && chatImagePath(node.url, workspaceRoot)) { pad(); pad(); append(label); }
        else if (chatFilePath(node.url, workspaceRoot)) append(label);
        else code(node, label);
      } else {
        if (transcriptMarks && node.kind === 'link' && markdownLinkProvider(node.url)) pad();
        read(node.children);
      }
    }
  }
  read(nodes);
  return decoration && end <= cursor ? selected : undefined;
}

export class MarkdownMediaRun {
  nodes: MarkdownInline[] = [];
  path: string;
  label: string;
  constructor(path: string = '', label: string = '') { this.path = path; this.label = label; }
}

/** Keep text and its styling on both sides of each image in source order. */
export function markdownMediaRuns(nodes: MarkdownInline[], cwd: string): MarkdownMediaRun[] {
  const runs: MarkdownMediaRun[] = [];
  function append(node: MarkdownInline): void {
    if (!runs.length || runs[runs.length - 1].path) runs.push(new MarkdownMediaRun());
    runs[runs.length - 1].nodes.push(node);
  }
  for (const node of nodes) {
    const path = node.kind === 'image' ? chatImagePath(node.url, cwd) : '';
    if (path) runs.push(new MarkdownMediaRun(path, markdownInlineText(node.children)));
    else if (node.children.length && node.kind !== 'image') {
      const children = markdownMediaRuns(node.children, cwd);
      if (!children.some(run => !!run.path)) append(node);
      else for (const child of children) {
        if (child.path) runs.push(child);
        else append(new MarkdownInline(node.kind, node.text, node.url, child.nodes, node.source));
      }
    } else append(node);
  }
  return runs;
}

export function markdownImagePaths(blocks: MarkdownBlock[], cwd: string): string[] {
  const paths = new Set<string>();
  function inline(nodes: MarkdownInline[]): void {
    for (const node of nodes) {
      if (node.kind === 'image' || node.kind === 'file') {
        const path = chatImagePath(node.url, cwd);
        if (path) paths.add(path);
      } else inline(node.children);
    }
  }
  function visit(list: MarkdownBlock[]): void {
    for (const block of list) {
      inline(block.inlines);
      for (const row of block.rows) for (const cell of row.cells) inline(cell);
      visit(block.children); visit(block.items);
    }
  }
  visit(blocks);
  return Array.from(paths);
}

export function structuredBubble(text: string): boolean {
  return text.includes('\n') || /^ {0,3}(?:>|#{1,6}\s|[-+*]\s|\d+[.)]\s|~~~|```)/.test(text);
}

function safeHttpUrl(value: string): boolean {
  if (!/^https?:\/\//i.test(value)) return false;
  if (/[\u0000-\u0020\u007f<>"'\\]/.test(value)) return false;
  const authority = value.replace(/^https?:\/\//i, '').split(/[/?#]/, 1)[0];
  const host = authority.slice(authority.lastIndexOf('@') + 1);
  return host.length > 0 && !host.startsWith(':') && !host.startsWith('.') && !host.endsWith('.');
}

function findBracketEnd(value: string, start: number): number {
  let depth = 0;
  for (let index = start + 1; index < value.length; index++) {
    if (value[index] === '\\') { index++; continue; }
    if (value[index] === '`') {
      const run = delimiterRun(value, index, '`');
      const close = findCodeEnd(value, index, run);
      if (close >= 0) { index = close + run - 1; continue; }
    }
    if (value[index] === '[') depth++;
    else if (value[index] === ']') {
      if (depth === 0) return index;
      depth--;
    }
  }
  return -1;
}

function linkCandidate(value: string, start: number): LinkCandidate | undefined {
  const closeLabel = findBracketEnd(value, start);
  if (closeLabel < 0 || value[closeLabel + 1] !== '(') return undefined;
  const urlStart = closeLabel + 2;
  let close = -1;
  if (value[urlStart] === '<') {
    const angleEnd = value.indexOf('>', urlStart + 1);
    if (angleEnd < 0 || value[angleEnd + 1] !== ')') return undefined;
    close = angleEnd + 1;
  } else {
    let depth = 0;
    for (let index = urlStart; index < value.length; index++) {
      if (value[index] === '\\') { index++; continue; }
      if (value[index] === '(') depth++;
      else if (value[index] === ')') {
        if (depth === 0) { close = index; break; }
        depth--;
      }
    }
    if (close < 0) return undefined;
  }
  const source = value.slice(start, close + 1);
  let url = value.slice(urlStart, close);
  if (url.startsWith('<') && url.endsWith('>')) url = url.slice(1, -1);
  const candidate = new LinkCandidate();
  candidate.end = close + 1;
  candidate.source = source;
  candidate.label = value.slice(start + 1, closeLabel);
  candidate.url = url;
  return candidate;
}

function delimiterRun(value: string, start: number, delimiter: string): number {
  let end = start;
  while (end < value.length && value[end] === delimiter) end++;
  return end - start;
}

function findCodeEnd(value: string, start: number, run: number): number {
  let index = start + run;
  while (index < value.length) {
    const next = value.indexOf('`', index);
    if (next < 0) return -1;
    const count = delimiterRun(value, next, '`');
    if (count === run) return next;
    index = next + count;
  }
  return -1;
}

function normalizeInlineCode(value: string): string {
  let result = value.replace(/\n/g, ' ');
  if (result.length > 2 && result.startsWith(' ') && result.endsWith(' ') && result.trim().length > 0)
    result = result.slice(1, -1);
  return result;
}

function escapedAt(value: string, index: number): boolean {
  let slashes = 0;
  for (let at = index - 1; at >= 0 && value[at] === '\\'; at--) slashes++;
  return slashes % 2 === 1;
}

function emphasisClose(value: string, start: number, delimiter: string): number {
  let index = start + delimiter.length;
  while (index < value.length) {
    if (value[index] === '\\') { index += 2; continue; }
    if (value[index] === '`') {
      const run = delimiterRun(value, index, '`');
      const close = findCodeEnd(value, index, run);
      if (close >= 0) { index = close + run; continue; }
    }
    const delimiterLength = delimiterRun(value, index, delimiter[0]);
    if (delimiterLength > 0) {
      if (escapedAt(value, index)) { index += delimiterLength; continue; }
      if (delimiterLength !== delimiter.length) { index += delimiterLength; continue; }
      const before = value[index - 1] || '';
      const after = value[index + delimiter.length] || '';
      if (before.trim().length > 0 && (delimiter.length > 1 || after !== delimiter[0])) return index;
      index += delimiterLength;
      continue;
    }
    index++;
  }
  return -1;
}

function isWordCharacter(value: string): boolean {
  return /^[A-Za-z0-9]$/.test(value);
}

function trimBareUrl(value: string): string {
  let result = value.replace(/[.,;:!?，。！？；：、"']+$/, '');
  let changed = true;
  while (changed) {
    changed = false;
    if (result.endsWith(')') && (result.match(/\)/g) || []).length > (result.match(/\(/g) || []).length) {
      result = result.slice(0, -1); changed = true;
    }
    if (result.endsWith(']') && (result.match(/\]/g) || []).length > (result.match(/\[/g) || []).length) {
      result = result.slice(0, -1); changed = true;
    }
    if (result.endsWith('}') && (result.match(/\}/g) || []).length > (result.match(/\{/g) || []).length) {
      result = result.slice(0, -1); changed = true;
    }
  }
  return result;
}

function bareUrlAt(value: string, start: number): string {
  if (start > 0 && isWordCharacter(value[start - 1])) return '';
  if (!value.startsWith('http://', start) && !value.startsWith('https://', start)) return '';
  let end = start;
  while (end < value.length && value.charCodeAt(end) <= 0x7e && !/[\s<>"'`]/.test(value[end])) end++;
  const url = trimBareUrl(value.slice(start, end));
  return safeHttpUrl(url) ? url : '';
}

function appendPlain(nodes: MarkdownInline[], text: string): void {
  if (!text) return;
  const last = nodes[nodes.length - 1];
  if (last && last.kind === 'text') {
    last.text += text;
    last.source += text;
  } else nodes.push(new MarkdownInline('text', text, '', [], text));
}

/** A Han, kana, Hangul or bopomofo letter; CJK punctuation is not one. */
function cjkLetter(ch: string | undefined): boolean {
  const code = ch === undefined ? 0 : ch.codePointAt(0) ?? 0;
  if (code === 0x30fb || code === 0x30fc) return false;
  return (code >= 0x1100 && code <= 0x11ff) || (code >= 0x3040 && code <= 0x30ff) || (code >= 0x3100 && code <= 0x318f) ||
    (code >= 0x31f0 && code <= 0x31ff) || (code >= 0x3400 && code <= 0x4dbf) || (code >= 0x4e00 && code <= 0x9fff) ||
    (code >= 0xac00 && code <= 0xd7af) || (code >= 0xf900 && code <= 0xfaff) || (code >= 0xff66 && code <= 0xff9f) ||
    (code >= 0x20000 && code <= 0x3ffff);
}

/** Mark inline code that meets a CJK letter directly, across emphasis and link edges (docs/design/DESIGN_SYSTEM.md 3.11). */
function markCodeGaps(nodes: MarkdownInline[]): MarkdownInline[] {
  const leaves: MarkdownInline[] = [];
  const walk = (parts: MarkdownInline[]): void => {
    for (const node of parts) {
      if (node.kind === 'bold' || node.kind === 'italic' || node.kind === 'link') walk(node.children);
      else leaves.push(node);
    }
  };
  walk(nodes);
  const words = (node: MarkdownInline | undefined): string[] => !node || node.kind === 'code' ? [] :
    Array.from(node.kind === 'text' ? node.text : markdownInlineText(node.children));
  leaves.forEach((node: MarkdownInline, index: number) => {
    if (node.kind !== 'code' && node.kind !== 'file') return;
    const before = words(leaves[index - 1]), after = words(leaves[index + 1]);
    node.gapBefore = cjkLetter(before[before.length - 1]);
    node.gapAfter = cjkLetter(after[0]);
  });
  return nodes;
}

/** A block's inline nodes, with the room inline code keeps beside CJK letters marked. */
function parseInlines(value: string): MarkdownInline[] {
  return markCodeGaps(parseInlineMarkdown(value));
}

export function parseInlineMarkdown(value: string): MarkdownInline[] {
  const nodes: MarkdownInline[] = [];
  let plain = '';
  const flush = (): void => { appendPlain(nodes, plain); plain = ''; };
  for (let index = 0; index < value.length;) {
    if (value[index] === '\\' && index + 1 < value.length && /[\\`*_[\]{}()#+.!<>|~-]/.test(value[index + 1])) {
      plain += value[index + 1];
      index += 2;
      continue;
    }
    if (value[index] === '`') {
      const run = delimiterRun(value, index, '`');
      const close = findCodeEnd(value, index, run);
      if (close >= 0) {
        flush();
        const end = close + run;
        const source = value.slice(index, end);
        nodes.push(new MarkdownInline('code', normalizeInlineCode(value.slice(index + run, close)), '', [], source));
        index = end;
        continue;
      }
    }
    const imagePrefix = value[index] === '!' && value[index + 1] === '[';
    const linkStart = imagePrefix ? index + 1 : index;
    if (value[linkStart] === '[') {
      const candidate = linkCandidate(value, linkStart);
      if (candidate) {
        const source = imagePrefix ? value.slice(index, candidate.end) : candidate.source;
        flush();
        if (imagePrefix && localImageDestination(candidate.url)) nodes.push(new MarkdownInline('image', '', candidate.url, parseInlineMarkdown(candidate.label), source));
        else if (!imagePrefix && safeHttpUrl(candidate.url)) nodes.push(new MarkdownInline('link', '', candidate.url, parseInlineMarkdown(candidate.label), source));
        else if (!imagePrefix && (markdownFilePath(candidate.url) || localImageDestination(candidate.url))) nodes.push(new MarkdownInline('file', '', candidate.url, parseInlineMarkdown(candidate.label), source));
        else appendPlain(nodes, source);
        index = candidate.end;
        continue;
      }
    }
    if (value[index] === '<') {
      const inertHtml = /^<(script|style|iframe|object|embed)\b[^>]*>[\s\S]*?<\/\1\s*>/i.exec(value.slice(index));
      if (inertHtml) {
        plain += inertHtml[0];
        index += inertHtml[0].length;
        continue;
      }
      const close = value.indexOf('>', index + 1);
      if (close > index) {
        const url = value.slice(index + 1, close);
        if (safeHttpUrl(url)) {
          flush();
          nodes.push(new MarkdownInline('link', '', url, [new MarkdownInline('text', url, '', [], url)], value.slice(index, close + 1)));
          index = close + 1;
          continue;
        }
      }
    }
    const bareUrl = bareUrlAt(value, index);
    if (bareUrl) {
      flush();
      nodes.push(new MarkdownInline('link', '', bareUrl, [new MarkdownInline('text', bareUrl, '', [], bareUrl)], bareUrl));
      index += bareUrl.length;
      continue;
    }
    let delimiter = '';
    let kind: MarkdownInlineKind = 'italic';
    if (value.startsWith('**', index) || value.startsWith('__', index)) {
      delimiter = value.slice(index, index + 2); kind = 'bold';
    } else if ((value[index] === '*' || value[index] === '_') && value[index + 1] !== value[index]) {
      delimiter = value[index]; kind = 'italic';
    }
    if (delimiter && !escapedAt(value, index)) {
      const next = value[index + delimiter.length] || '';
      const before = value[index - 1] || '';
      const underscoreInWord = delimiter === '_' && isWordCharacter(before) && isWordCharacter(next);
      const close = next.trim().length > 0 && !underscoreInWord ? emphasisClose(value, index, delimiter) : -1;
      if (close > index + delimiter.length) {
        flush();
        const end = close + delimiter.length;
        const source = value.slice(index, end);
        nodes.push(new MarkdownInline(kind, '', '', parseInlineMarkdown(value.slice(index + delimiter.length, close)), source));
        index = end;
        continue;
      }
      plain += delimiter;
      index += delimiter.length;
      continue;
    }
    plain += value[index];
    index++;
  }
  flush();
  return nodes;
}

class MarkdownParser {
  private lines: string[] = [];
  private index: number = 0;

  constructor(source: string) { this.lines = source.split('\n'); }

  parse(depth: number = 0): MarkdownBlock[] {
    if (depth > 24) {
      const source = this.lines.join('\n');
      return source ? [new MarkdownBlock('raw', source)] : [];
    }
    const blocks: MarkdownBlock[] = [];
    while (this.index < this.lines.length) {
      const line = this.lines[this.index];
      if (!line.trim()) { this.index++; continue; }
      const fence = fenceMarker(line);
      if (fence) { blocks.push(this.readFence(fence)); continue; }
      const heading = headingMatch(line);
      if (heading) {
        const block = new MarkdownBlock('heading', line);
        block.level = heading[2].length;
        block.inlines = parseInlines(headingBody(heading[3]));
        blocks.push(block); this.index++; continue;
      }
      if (isRule(line)) { blocks.push(new MarkdownBlock('rule', line)); this.index++; continue; }
      if (quotePrefix(line)) { blocks.push(this.readQuote(depth)); continue; }
      const table = this.readTable();
      if (table) { blocks.push(table); continue; }
      if (listMarker(line)) { blocks.push(this.readList(depth)); continue; }
      blocks.push(this.readParagraph());
    }
    return blocks;
  }

  private readFence(fence: FenceMarker): MarkdownBlock {
    const start = this.index;
    let close = start + 1;
    while (close < this.lines.length && !isFenceClose(this.lines[close], fence)) close++;
    if (close >= this.lines.length) {
      this.index = this.lines.length;
      const source = this.lines.slice(start).join('\n');
      const raw = new MarkdownBlock('raw', source); raw.text = source; return raw;
    }
    const source = this.lines.slice(start, close + 1).join('\n');
    const block = new MarkdownBlock('code', source);
    block.language = fence.language;
    const codeLines = this.lines.slice(start + 1, close);
    block.text = codeLines.length > 0 ? codeLines.join('\n') + '\n' : '';
    this.index = close + 1;
    return block;
  }

  private readQuote(depth: number): MarkdownBlock {
    const start = this.index;
    const quoted: string[] = [];
    while (this.index < this.lines.length) {
      const prefix = quotePrefix(this.lines[this.index]);
      if (!prefix) break;
      quoted.push(this.lines[this.index].slice(prefix[0].length));
      this.index++;
    }
    const block = new MarkdownBlock('quote', this.lines.slice(start, this.index).join('\n'));
    block.children = new MarkdownParser(quoted.join('\n')).parse(depth + 1);
    return block;
  }

  private readList(depth: number): MarkdownBlock {
    const start = this.index;
    const first = listMarker(this.lines[this.index]);
    const block = new MarkdownBlock('list');
    if (!first) return block;
    block.ordered = first.ordered;
    while (this.index < this.lines.length) {
      const marker = listMarker(this.lines[this.index]);
      if (!marker || marker.indent !== first.indent || marker.ordered !== first.ordered) break;
      const itemStart = this.index;
      const itemLines: string[] = [marker.content];
      const item = new MarkdownBlock('list-item');
      item.marker = marker.ordered ? marker.marker : '•';
      this.index++;
      while (this.index < this.lines.length) {
        const nextLine = this.lines[this.index];
        const nextMarker = listMarker(nextLine);
        if (nextMarker && nextMarker.indent === first.indent) break;
        if (!nextLine.trim()) {
          const following = this.index + 1 < this.lines.length ? listMarker(this.lines[this.index + 1]) : undefined;
          if (following && following.indent === first.indent && following.ordered === first.ordered) break;
          if (this.index + 1 < this.lines.length && leadingSpaces(this.lines[this.index + 1]) >= marker.contentIndent) {
            itemLines.push(''); this.index++; continue;
          }
          break;
        }
        const indent = leadingSpaces(nextLine);
        if (indent < marker.contentIndent) break;
        itemLines.push(nextLine.slice(marker.contentIndent));
        this.index++;
      }
      item.source = this.lines.slice(itemStart, this.index).join('\n');
      item.children = new MarkdownParser(itemLines.join('\n')).parse(depth + 1);
      block.items.push(item);
    }
    block.source = this.lines.slice(start, this.index).join('\n');
    return block;
  }

  private readTable(): MarkdownBlock | undefined {
    const start = this.index;
    if (start + 1 >= this.lines.length || !hasTableSeparator(this.lines[start])) return undefined;
    const headerCells = splitTableCells(this.lines[start]);
    const alignments = tableAlignments(this.lines[start + 1], headerCells.length);
    if (!alignments) return undefined;
    const block = new MarkdownBlock('table');
    block.alignments = alignments;
    const header = new MarkdownTableRow();
    header.header = true;
    header.cells = headerCells.map((cell: string) => parseInlines(cell));
    block.rows.push(header);
    this.index += 2;
    while (this.index < this.lines.length && this.lines[this.index].trim() && hasTableSeparator(this.lines[this.index])) {
      const cells = splitTableCells(this.lines[this.index]);
      const row = new MarkdownTableRow();
      row.cells = headerCells.map((_label: string, index: number) => parseInlines(cells[index] || ''));
      block.rows.push(row);
      this.index++;
    }
    block.source = this.lines.slice(start, this.index).join('\n');
    return block;
  }

  private readParagraph(): MarkdownBlock {
    const start = this.index;
    this.index++;
    while (this.index < this.lines.length && this.lines[this.index].trim() && !this.isBlockStart(this.index)) this.index++;
    const source = this.lines.slice(start, this.index).join('\n');
    const block = new MarkdownBlock('paragraph', source);
    block.inlines = parseInlines(source);
    return block;
  }

  private isBlockStart(index: number): boolean {
    const line = this.lines[index];
    return !!fenceMarker(line) || !!headingMatch(line) || isRule(line) || !!quotePrefix(line) || !!listMarker(line) ||
      (index + 1 < this.lines.length && hasTableSeparator(line) && !!tableAlignments(this.lines[index + 1], splitTableCells(line).length));
  }
}

function leadingSpaces(value: string): number {
  const match = /^ */.exec(value);
  return match ? match[0].length : 0;
}

export function parseMarkdown(source: string): MarkdownBlock[] {
  return new MarkdownParser(source.replace(/\r\n?/g, '\n')).parse();
}

export function markdownInlineText(nodes: MarkdownInline[]): string {
  let result = '';
  for (const node of nodes) {
    if (node.kind === 'text' || node.kind === 'code') result += node.text;
    else result += markdownInlineText(node.children);
  }
  return result;
}

/** Allocate one set of column widths for the whole table. Short values leave space for prose. */
export function markdownTableWidths(measured: readonly number[], viewport: number, fontScale: number): number[] {
  if (!measured.length) return [];
  const scale = Math.max(1, fontScale);
  const ideal = measured.map(width => Math.max(52 * scale, Math.min(310 * scale, width + 18)));
  const wide = measured.length >= 4;
  const minimum = ideal.map(width => Math.min(width, wide
    ? Math.max(96 * scale, Math.min(148 * scale, width * 0.85))
    : Math.max(66 * scale, Math.min(128 * scale, width * 0.62))));
  const total = (widths: number[]): number => widths.reduce((a, b) => a + b, 0);
  const floor = total(minimum), preferred = total(ideal);
  if (viewport <= floor) return minimum;
  if (viewport >= preferred) return ideal.map(width => width * viewport / preferred);
  const fraction = (viewport - floor) / (preferred - floor);
  return minimum.map((width, index) => width + (ideal[index] - width) * fraction);
}

export function markdownVisibleText(blocks: MarkdownBlock[]): string {
  const pieces: string[] = [];
  for (const block of blocks) {
    if (block.kind === 'paragraph' || block.kind === 'heading') pieces.push(markdownInlineText(block.inlines));
    else if (block.kind === 'code' || block.kind === 'raw') pieces.push(block.text);
    else if (block.kind === 'quote') pieces.push(markdownVisibleText(block.children));
    else if (block.kind === 'list') {
      for (const item of block.items) pieces.push(markdownVisibleText(item.children));
    } else if (block.kind === 'table') {
      for (const row of block.rows) pieces.push(row.cells.map((cell: MarkdownInline[]) => markdownInlineText(cell)).join('\t'));
    }
  }
  return pieces.join('\n');
}

/** A reading view owns this small cache; immutable syntax trees survive leaving and reentering the viewport. */
export class ConversationMarkdownCache {
  private documents = new Map<string, MarkdownBlock[]>();
  private characters = 0;
  parse(source: string): MarkdownBlock[] {
    const hit = this.documents.get(source);
    if (hit) {
      this.documents.delete(source); this.documents.set(source, hit);
      return hit;
    }
    const blocks = parseMarkdown(source);
    if (!source || source.length > 128 * 1024) return blocks;
    while (this.documents.size >= 32 || this.characters + source.length > 128 * 1024) {
      const first = this.documents.keys().next().value as string;
      this.documents.delete(first); this.characters -= first.length;
    }
    this.documents.set(source, blocks); this.characters += source.length;
    return blocks;
  }
}

/** The selectable words of a lifted chat message. A table copies as its original Markdown. */
export function liftedMessageWords(source: string, plain: boolean): string {
  if (plain) return source;
  function words(block: MarkdownBlock): string {
    if (block.kind === 'table') return block.source;
    if (block.kind === 'list') return block.items.map((item: MarkdownBlock) =>
      (block.ordered ? item.marker : '•') + ' ' + item.children.map(words).join('\n')).join('\n');
    if (block.kind === 'quote') return block.children.map(words).join('\n');
    return markdownVisibleText([block]);
  }
  return parseMarkdown(source).map(words).filter((text: string) => text.length > 0).join('\n\n');
}
