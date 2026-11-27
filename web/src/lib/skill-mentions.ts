import { findLeadingContextBlocks } from "./compatibility";
import { unescapeContextBody } from "./context-body";
import { contextXML } from "./context-xml";
import type { ContextRef } from "./links";
import { tagAttrs, type Context } from "./turns";

export interface MentionSkill {
  id: string;
  name: string;
  description?: string;
  source: string;
}

export interface SkillCatalog {
  catalog_version: number;
  skills: MentionSkill[];
  complete: boolean;
}

/** Execution environment for a picker's catalog. Ordinary Agents use session-owned facts. */
export interface SkillSource {
  manager: string;
  session?: string;
  launcher?: string;
  environment: string;
  agent?: boolean;
  /** Scheduled tasks resolve discovery against their unsaved execution settings. */
  catalog?: (signal: AbortSignal) => Promise<SkillCatalog>;
}

export function matchingSkills(skills: MentionSkill[], query: string): MentionSkill[] {
  const q = query.toLocaleLowerCase();
  return skills.filter((s) => `${s.name}\n${s.description ?? ""}`.toLocaleLowerCase().includes(q));
}

export function skillContext(skill: MentionSkill): ContextRef {
  return { id: `skill:${skill.id}`, kind: "skill", title: skill.name, url: "", text: [skill.source, skill.description].filter(Boolean).join("\n"), attrs: { ref: skill.id } };
}

/** The same opaque reference travels in drafts, outbox/retry, queued text and history. Never infer one from @text. */
export function skillDraft(refs: ContextRef[], text: string): string {
  const skills = refs.filter((r) => r.kind === "skill");
  return skills.length ? `${skills.map(contextXML).join("\n")}\n\n${text}` : text;
}

export function skillEditText(contexts: Context[], text: string): string {
  return skillDraft(contexts.filter((c) => c.kind === "skill").map((c) => ({
    id: `skill:${c.attrs?.ref}`, kind: "skill", title: c.title ?? "", url: c.url ?? "", text: c.text, attrs: c.attrs,
  })), text);
}

const scheduledSeparator = /^[ \t\r]*\n[ \t\r]*\n/;
const asciiSpaceOnly = /^[ \t\r\n\f]*$/;

/** Pull only leading explicit selections back into removable chips. Keep existing non-skill context bytes. */
export function splitSkillDraft(raw: string, includeFiles = false): { text: string; skills: ContextRef[] } {
  const skills: ContextRef[] = [];
  const kept: string[] = [];
  const { blocks } = findLeadingContextBlocks(raw);
  let pos = 0;
  for (const block of blocks) {
    const gap = raw.slice(pos, block.start);
    if (includeFiles && !asciiSpaceOnly.test(gap)) break;
    if (includeFiles && pos > 0) {
      // Match promptcontent.Split: two line breaks in leading spaces, tabs, CRs and LFs end the selections.
      const separator = scheduledSeparator.exec(gap);
      if (separator) {
        pos += separator[0].length;
        break;
      }
    }
    const { kind, title, url, ...attrs } = tagAttrs(block.attrs);
    if ((kind === "skill" && attrs.ref) || (includeFiles && kind === "file" && attrs.path)) {
      const id = `${kind}:${kind === "file" ? attrs.path : attrs.ref}`;
      if (!skills.some((s) => s.id === id)) skills.push({ id, kind, title: title ?? "", url: url ?? "", text: unescapeContextBody(block.body).trim(), attrs });
    } else if (kind !== "skill-instructions") {
      kept.push(raw.slice(block.start, block.end).trim());
    }
    pos = block.end;
  }
  let rest = raw.slice(pos);
  if (includeFiles && pos > 0) {
    const separator = scheduledSeparator.exec(rest);
    if (separator) rest = rest.slice(separator[0].length);
  }
  if (!skills.length && !raw.includes('kind="skill-instructions"')) return { text: raw, skills };
  const body = includeFiles ? rest : rest.trimStart();
  return { text: [...kept, body].filter(Boolean).join("\n\n"), skills };
}
