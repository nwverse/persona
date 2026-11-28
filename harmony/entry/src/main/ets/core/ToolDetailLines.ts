export type ToolLineKind = 'code' | 'add' | 'remove' | 'hunk' | 'file';
export interface ToolDetailLine { kind: ToolLineKind; number: string; text: string; }

/** Bound both row count and a single long line. Clipboard actions use the original field. */
export function toolDetailPage(text: string, pages: number): string {
  const count = Math.max(1, pages);
  const head = text.slice(0, count * 6000);
  let end = 0;
  for (let i = 0; i < count * 50 && end < head.length; i++) {
    const next = head.indexOf('\n', end);
    end = next < 0 ? head.length : next + 1;
  }
  if (end < text.length && end > 0 && /[\uD800-\uDBFF]/.test(head[end - 1])) end--;
  return head.slice(0, end);
}

function linesOf(text: string): string[] {
  const lines = text.split(/\r?\n/);
  if (lines[lines.length - 1] === '') lines.pop();
  return lines;
}

export function sourceDetailLines(text: string, first?: number): ToolDetailLine[] {
  const lines = linesOf(text);
  const numbered = lines.map(line => /^\s*(\d+)(?:\t|→)(.*)$/.exec(line));
  if (numbered.length && numbered.every(match => !!match)) {
    return numbered.map(match => ({ kind: 'code', number: match![1], text: match![2] }));
  }
  return lines.map((line, i) => ({ kind: 'code', number: first === undefined ? '' : String(first + i), text: line }));
}

/** Group only consecutive, unambiguous file:line:content records. Preserve an unfamiliar format verbatim. */
export function searchDetailLines(text: string): ToolDetailLine[] {
  const rows: ToolDetailLine[] = [];
  let lastPath = '';
  for (const line of linesOf(text)) {
    if (!line.trim()) continue;
    const match = /^(.+?):([1-9]\d*):(.*)$/.exec(line);
    if (!match || /^\d+$/.test(match[1]) || match[1].includes('://')) return [];
    if (lastPath !== match[1]) {
      lastPath = match[1];
      rows.push({ kind: 'file', number: '', text: lastPath });
    }
    rows.push({ kind: 'code', number: match[2], text: match[3] });
  }
  return rows;
}

export function diffDetailLines(text: string): ToolDetailLine[] {
  let oldLine = 0, newLine = 0;
  let oldRemaining = 0, newRemaining = 0;
  return linesOf(text).map((line, index, lines): ToolDetailLine => {
    const hunk = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/.exec(line);
    if (hunk) {
      oldLine = Number(hunk[1]);
      newLine = Number(hunk[3]);
      oldRemaining = Number(hunk[2] ?? '1');
      newRemaining = Number(hunk[4] ?? '1');
      return { kind: 'hunk', number: '', text: line };
    }
    const header = (line.startsWith('--- ') && lines[index + 1]?.startsWith('+++ ')) ||
      (line.startsWith('+++ ') && lines[index - 1]?.startsWith('--- '));
    if (line.startsWith('diff --git ') || (oldRemaining === 0 && newRemaining === 0 && header)) {
      oldRemaining = 0;
      newRemaining = 0;
      return { kind: 'file', number: '', text: line };
    }
    if (line.startsWith('+')) {
      const number = newRemaining > 0 ? String(newLine++) : '';
      if (newRemaining > 0) newRemaining--;
      return { kind: 'add', number, text: line.slice(1) };
    }
    if (line.startsWith('-')) {
      const number = oldRemaining > 0 ? String(oldLine++) : '';
      if (oldRemaining > 0) oldRemaining--;
      return { kind: 'remove', number, text: line.slice(1) };
    }
    if (line.startsWith(' ') && oldRemaining > 0 && newRemaining > 0) {
      oldLine++;
      oldRemaining--;
      newRemaining--;
      return { kind: 'code', number: String(newLine++), text: line.slice(1) };
    }
    return { kind: 'code', number: '', text: line };
  });
}
