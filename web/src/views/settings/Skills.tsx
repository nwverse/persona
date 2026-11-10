// Settings › Assistant › Skills (the app's SkillsPage, docs/operations/hub-update.md "Skills move out of the binary"): the assistant's skills on
// the computer that runs it (GET /api/v1/skills), read only.
import { useEffect, useState } from "react";
import { t } from "../../i18n";
import { errorText } from "../../lib/api";
import { skills as loadSkills, type Skill } from "../../lib/hub-update";
import { Loading } from "../../ui/atoms";
import { Group, Pill } from "./SettingsParts";

export function SkillsGroup({ manager }: { manager?: string } = {}) {
  const [list, setList] = useState<Skill[] | null>(null);
  const [machine, setMachine] = useState<string>();
  const [error, setError] = useState("");

  useEffect(() => {
    loadSkills(manager)
      .then((r) => (setList(r.skills ?? []), setMachine(r.machine || r.machine_id)))
      .catch((e) => (setError(errorText(e)), setList([])));
  }, []);

    return (
    <Group
      title={
        <span className="flex items-center gap-2">
          Skills
          {list && list.length > 0 && <span className="font-normal tabular-nums">{t("settings-skills.count", { count: list.length })}</span>}
        </span>
      }
      footer={list?.length ? (machine ? t("settings-skills.footer_machine", { machine }) : t("settings-skills.footer")) : undefined}
    >
      {list === null ? (
        <div className="px-4 py-4">
          <Loading />
        </div>
      ) : list.length === 0 ? (
        <p className="px-4 py-4 text-[12.5px] leading-relaxed text-ink-3">
          {error ? t("settings-skills.load_failed", { error }) : t("settings-skills.empty")}
        </p>
      ) : (
        list.map((s) => (
          <div key={s.name} className="px-4 py-2.5">
            <div className="flex items-center gap-2">
              <span className="min-w-0 flex-1 truncate text-[13px] font-medium text-ink">{s.name}</span>
              <Pill tone={s.source === "builtin" ? undefined : "accent"}>{s.source === "builtin" ? t("settings-skills.source_builtin") : t("settings-skills.source_mine")}</Pill>
            </div>
            {s.description && <p className="mt-0.5 line-clamp-3 text-[11.5px] leading-snug text-ink-3">{s.description}</p>}
          </div>
        ))
      )}
    </Group>
  );
}
