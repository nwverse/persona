// Skill-aware second stage of the / palette, loaded only after the palette opens.
import { useImperativeHandle, useMemo } from "react";
import { rankCommands, SLASH_LIMIT } from "../../lib/composer-commands";
import type { ComposerCommand as Command } from "../../lib/extension-prompts";
import type { MentionSkill } from "../../lib/skill-mentions";
import { SkillRow } from "./SkillRow";
import { SkillCatalogStatus } from "./SkillCatalogStatus";
import { useSkillCatalog } from "./useSkillCatalog";
import { SlashPaletteView, type SlashPaletteInput } from "./SlashPalette";

type SkillCommand = Command & { skill: MentionSkill };
const attached = (row: Command | undefined) => row && (row as SkillCommand).skill;

export function SlashSkillPalette(input: SlashPaletteInput) {
  const { source, machineId } = input;
  const query = input.query ?? "";
  const catalog = useSkillCatalog(source?.skills, machineId, source?.cwd);
  const skillRows = useMemo(() => {
    const remaining = SLASH_LIMIT - input.rows.length;
    if (remaining <= 0) return [];
    const candidates: SkillCommand[] = (catalog.catalog?.skills ?? []).map((skill) => ({
      key: `skill:${skill.id}`, word: skill.name, desc: skill.description ?? "", text: skill.name, source: skill.source, skill,
    }));
    return rankCommands(candidates, query, remaining);
  }, [catalog.catalog, query, input.rows.length]);
  const entries = useMemo<Command[]>(() => [...input.rows, ...skillRows], [input.rows, skillRows]);
  const current = Math.min(input.active, entries.length - 1);
  const pick = (row: Command) => {
    const skill = attached(row);
    if (skill) input.onSkillPick?.(skill);
    else input.onPick(row);
  };
  useImperativeHandle(input.handle, () => ({
    skillActive: () => Boolean(attached(entries[current])),
    key: (key: string): boolean => {
      if (!entries.length) return false;
      if (key === "ArrowDown" || key === "ArrowUp") {
        input.onHover((current + (key === "ArrowDown" ? 1 : entries.length - 1)) % entries.length);
        return true;
      }
      if (key === "Enter" || key === "Tab") {
        pick(entries[current]);
        return true;
      }
      return false;
    },
  }));
  return (
    <SlashPaletteView
      {...input}
      entries={entries}
      onPick={pick}
      status={<SkillCatalogStatus status={catalog.status} />}
      skillsLoading={catalog.status === "loading"}
      renderRow={(row, common) => {
        const skill = attached(row);
        return skill && <SkillRow key={skill.id} skill={skill} common={common} compact />;
      }}
    />
  );
}
