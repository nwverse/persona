// Skill-aware second stage of the / palette, loaded only after the palette opens.
import { useMemo, type ComponentType } from "react";
import { rankCommands, SLASH_LIMIT } from "../../lib/composer-commands";
import { SkillCatalogStatus, SkillRow } from "./SkillRow";
import { useSkillCatalog } from "./useSkillCatalog";
import type { Entry, SlashPaletteInput, SlashPaletteViewProps } from "./SlashPalette";

export function SlashSkillPalette(props: SlashPaletteInput & { View: ComponentType<SlashPaletteViewProps> }) {
  const { View, ...input } = props;
  const { source, machineId } = input;
  const query = input.query ?? "";
  const catalog = useSkillCatalog(source?.skills, machineId, source?.cwd);
  const skillRows = useMemo(() => {
    const candidates = (catalog.catalog?.skills ?? []).map((skill) => ({ word: skill.name, desc: skill.description ?? "", skill }));
    return rankCommands(candidates, query, Math.max(0, SLASH_LIMIT - input.rows.length));
  }, [catalog.catalog, query, input.rows.length]);
  const entries = useMemo<Entry[]>(() => [
    ...input.rows.map((command): Entry => ({ kind: "command", command })),
    ...skillRows.map(({ skill }): Entry => ({ kind: "skill", skill })),
  ], [input.rows, skillRows]);
  return (
    <View
      {...input}
      entries={entries}
      status={<SkillCatalogStatus status={catalog.status} />}
      skillsLoading={catalog.status === "loading"}
      renderSkill={(skill, common) => <SkillRow key={skill.id} skill={skill} common={common} compact />}
    />
  );
}
