import { Wire } from './Wire';

function attribute(value: string): string {
  return value.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

// Naming literals are confined to the compatibility boundary, as on the other clients.
export function withSourceContext(text: string, context: string, features: string[]): string {
  if (!context) return text;
  const data = Wire.parse(context);
  const tag = features.includes('persona_contracts_v1') ? 'persona-context' : 'jarvis-context';
  const entries = Wire.rows(data, 'contexts');
  const sources = entries.length ? entries : [data];
  return sources.map((source: Record<string, Object>) => '<' + tag + ' kind="' + attribute(Wire.text(source, 'kind', 'context')) +
    '" title="' + attribute(Wire.text(source, 'title')) + '">' +
    JSON.stringify(source).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;') + '</' + tag + '>').join('\n') + '\n\n' + text;
}

export class MessageDisplay {
  text: string = '';
  sources: string = '';
}
export function messageDisplay(input: string): MessageDisplay {
  const result = new MessageDisplay(); result.text = input;
  const titles: string[] = []; let remaining = input;
  for (let index = 0; index < 32; index++) {
    const match = remaining.match(/^<(persona-context|jarvis-context)\b([^>]*)>[\s\S]*?<\/(persona-context|jarvis-context)>\s*/);
    if (!match || match[1] !== match[3]) break;
    const title = match[2].match(/\btitle="([^"]*)"/);
    if (title) titles.push(title[1].replace(/&quot;/g, '"').replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&'));
    remaining = remaining.slice(match[0].length);
  }
  if (remaining !== input) { result.text = remaining; result.sources = titles.join(' · '); }
  return result;
}
