// Small native text runs for common chat fences. Copy always uses the original fence body.
export type CodeInk = 'plain' | 'keyword' | 'string' | 'literal' | 'comment' | 'added' | 'removed';
export interface CodeRun { offset: number; ink: CodeInk; text: string; }

const languageNames = new Map<string, string>([
  ['ts', 'TypeScript'], ['typescript', 'TypeScript'], ['js', 'JavaScript'], ['javascript', 'JavaScript'],
  ['py', 'Python'], ['python', 'Python'], ['json', 'JSON'], ['sql', 'SQL'], ['sh', 'Shell'], ['bash', 'Bash'], ['go', 'Go'], ['diff', 'Diff']
]);
export function chatCodeLanguageLabel(language: string): string { return languageNames.get(language.toLowerCase()) || language; }

const cLike = new Set('ts tsx typescript js jsx javascript arkts ets go golang rust rs java kt kotlin c h cpp c++ swift'.split(' '));
const keywords = new Set(('async await break case catch class const continue default defer def delete do elif else enum except ' +
  'export extends false finally fn for from func function if impl import in interface let match new None null package ' +
  'private public raise return select self static struct switch throw throws trait true try type typeof undefined ' +
  'var void while with yield as of is pass True False string boolean number int bool val fun go range').split(' '));
const shellKeys = new Set('if then else elif fi case esac for do done in while until function export local return'.split(' '));
const sqlKeys = new Set('select from where join left right inner outer on group by limit order asc desc having insert into values update set delete create table as and or is not null true false distinct union all'.split(' '));
const cTokens = /\/\/[^\n]*|\/\*[\s\S]*?(?:\*\/|$)|"(?:\\[\s\S]|[^"\\])*"?|'(?:\\[\s\S]|[^'\\])*'?|`(?:\\[\s\S]|[^`\\])*`?|\b(?:0[xX][\da-fA-F]+|\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)\b|[a-zA-Z_$][\w$]*/g;
const hashTokens = /#[^\n]*|"""[\s\S]*?(?:"""|$)|'''[\s\S]*?(?:'''|$)|"(?:\\[\s\S]|[^"\\])*"?|'(?:\\[\s\S]|[^'\\])*'?|\$[a-zA-Z_]\w*|\b\d+(?:\.\d+)?\b|[a-zA-Z_]\w*/g;
const sqlTokens = /--[^\n]*|\/\*[\s\S]*?(?:\*\/|$)|'(?:''|[^'])*'?|"(?:""|[^"])*"?|\b\d+(?:\.\d+)?\b|[a-zA-Z_]\w*/g;
const plain = (text: string): CodeRun[] => [{ offset: 0, ink: 'plain', text }];

export function chatCodeRuns(text: string, language: string): CodeRun[] {
  if (!text || text.length > 16000) return plain(text);
  const lang = language.toLowerCase().trim().split(/\s+/, 1)[0];
  const runs: CodeRun[] = [];
  const append = (offset: number, value: string, ink: CodeInk): void => {
    if (!value) return;
    const last = runs[runs.length - 1];
    if (last?.ink === ink) last.text += value;
    else runs.push({ offset, text: value, ink });
  };
  if (lang === 'diff' || lang === 'patch') {
    let at = 0;
    for (const line of text.match(/[^\n]*\n|[^\n]+$/g) || []) {
      append(at, line, line.startsWith('+') ? 'added' : line.startsWith('-') ? 'removed' : line.startsWith('@@') ? 'keyword' : 'plain');
      at += line.length;
      if (runs.length > 500) return plain(text);
    }
    return runs;
  }
  const shell = ['sh','bash','zsh','shell'].includes(lang), python = ['py','python'].includes(lang);
  const sql = lang === 'sql', json = lang === 'json' || lang === 'jsonc';
  if (!shell && !python && !sql && !json && !cLike.has(lang)) return plain(text);
  const grammar = sql ? sqlTokens : shell || python ? hashTokens : cTokens;
  const words = sql ? sqlKeys : shell ? shellKeys : keywords;
  const tokens = new RegExp(grammar.source, 'g');
  let match: RegExpExecArray | null, cursor = 0;
  while ((match = tokens.exec(text)) !== null) {
    const value = match[0], at = match.index;
    let ink: CodeInk = 'plain';
    if (value.startsWith('//') || value.startsWith('/*') || value.startsWith('#') || value.startsWith('--')) ink = 'comment';
    else if (['"', "'", '`'].includes(value[0])) {
      ink = json && /^\s*:/.test(text.slice(tokens.lastIndex)) ? 'keyword' : 'string';
    } else if (/^\d/.test(value) || ['true','false','null','None','True','False'].includes(value) || (shell && value.startsWith('$'))) ink = 'literal';
    else if (!json && words.has(sql ? value.toLowerCase() : value)) ink = 'keyword';
    append(cursor, text.slice(cursor, at), 'plain');
    append(at, value, ink);
    cursor = at + value.length;
    if (runs.length > 500) return plain(text);
  }
  append(cursor, text.slice(cursor), 'plain');
  return runs;
}
