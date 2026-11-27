// Settings › General › Templates (#16772, lib/templates.ts): the built-in templates with what each changes, Use → a
// preview of every change (current → new, what is skipped and why) → Apply in one tap, Share → a link that opens the
// same preview on another Persona (`/settings?template=…`), and Import for such a link or a template's JSON. The last
// template applied here is remembered in this browser so its row says In use.
import { useEffect, useMemo, useState } from "react";
import { currentLanguage, t } from "../../i18n";
import { api, ApiError } from "../../lib/api";
import { extensionDetail, setExtensionConfigPreservingEntries, setExtensionEnabled, type JsonObject } from "../../lib/extensions";
import { look, setLook } from "../../lib/looks";
import { phoneNavMode, setPhoneNavMode, type PhoneNavMode } from "../../lib/phone-nav";
import { setSidebarIconMode, sidebarIconMode, type SidebarIconMode } from "../../lib/sidebar-icons";
import { getSidebarPrefs, resetSidebarPrefs, restoreSidebarPrefs } from "../../lib/sidebar-pages";
import { saveStartPage, startPage } from "../../lib/start-page";
import {
  applyTemplate, builtInTemplates, localized, planTemplate, readTemplateInput, TEMPLATE_KEY, templateLink,
  type PersonaTemplate, type TemplateHost, type TemplateItem, type TemplateSetting,
} from "../../lib/templates";
import { moduleSwitchedOff, setModuleSwitchedOff } from "../../core/module-switch";
import { registeredModules } from "../../core/module-host";
import { Button, copyText, Dialog, toast } from "../../ui/atoms";
import { Group, Row } from "./SettingsParts";

const tt = (k: string, args?: Record<string, string | number>) => t(`settings-templates.${k}`, args);

function readApplied(): { id: string; name: string } | null {
  try {
    const v = JSON.parse(localStorage.getItem(TEMPLATE_KEY) ?? "null");
    return v && typeof v.id === "string" && typeof v.name === "string" ? v : null;
  } catch {
    return null;
  }
}

function keepApplied(t: PersonaTemplate, name: string) {
  try {
    localStorage.setItem(TEMPLATE_KEY, JSON.stringify({ id: t.id, name }));
  } catch {
    // not kept: the row just won't say In use
  }
}

/** The real reads and writes a template's plan and apply go through. */
export function templateHost(): TemplateHost {
  return {
    phoneNav: phoneNavMode,
    setPhoneNav: setPhoneNavMode,
    sidebarIcons: sidebarIconMode,
    setSidebarIcons: setSidebarIconMode,
    look,
    setLook,
    modules: () => registeredModules().map((d) => ({ id: d.id, title: d.title(), off: moduleSwitchedOff(d.id) })),
    setModuleOff: (id, off) => {
      if (!setModuleSwitchedOff(id, off)) throw new Error(tt("storage_failed"));
    },
    startPage,
    saveStartPage: async (key) => {
      const e = await saveStartPage(key);
      if (e) throw e;
    },
    sidebarPrefs: getSidebarPrefs,
    setSidebarPrefs: (p) => (p ? restoreSidebarPrefs(p) : resetSidebarPrefs()),
    extension: async (id) => {
      try {
        const d = await extensionDetail(id);
        return { name: d.name || id, enabled: d.enabled !== false, config: (d.config ?? {}) as JsonObject };
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) return undefined;
        throw e;
      }
    },
    setExtensionEnabled: (id, on) => setExtensionEnabled(id, on),
    setExtensionConfig: (id, config) => setExtensionConfigPreservingEntries(id, config),
    settings: async (items: TemplateSetting[], dryRun: boolean) => {
      try {
        const r = await api<{ results: { key: string; title?: string; status: string; text?: string; error?: string }[] }>("/templates/settings", { method: "POST", json: { settings: items, dry_run: dryRun } });
        return r.results;
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) return undefined; // a hub from before templates
        throw e;
      }
    },
    words: {
      phoneNav: tt("item_phone_nav"),
      phoneNavMode: (m: PhoneNavMode) => tt(`phone_nav_${m}`),
      sidebarIcons: t("ui-look.sidebar_icons"),
      sidebarIconsMode: (m: SidebarIconMode) => t(`ui-look.sidebar_icons_${m}`),
      look: (name) => tt(`item_look_${name}`),
      lookMode: (name, mode) => tt(`look_${name}_${mode}`),
      module: (title) => tt("item_module", { title }),
      on: tt("on"),
      off: tt("off"),
      startPage: tt("item_start_page"),
      startDefault: tt("start_default"),
      sidebarPages: tt("item_sidebar_pages"),
      pagesDefault: tt("pages_default"),
      pagesUnderMore: (n) => tt("pages_under_more", { count: n }),
      pagesCustom: tt("pages_custom"),
      extensionMissing: tt("extension_missing"),
      extensionEnabled: tt("extension_enabled"),
      extensionConfig: (keys) => tt("extension_config", { keys }),
      settingsUnsupportedHub: tt("settings_unsupported_hub"),
      settingStatus: (status) => tt(`setting_${status}`),
    },
  };
}

interface Open {
  template: PersonaTemplate;
  builtIn: boolean;
  fromLink: boolean;
  items?: TemplateItem[];
  result?: TemplateItem[];
  error?: string;
  busy?: boolean;
}

export function TemplatesGroup() {
  const builtIns = useMemo(() => builtInTemplates((k) => tt(k)), []);
  const [applied, setApplied] = useState(readApplied);
  const [open, setOpen] = useState<Open | null>(null);
  const [importText, setImportText] = useState("");
  const [importError, setImportError] = useState("");
  const lang = currentLanguage();

  const preview = async (template: PersonaTemplate, builtIn: boolean, fromLink: boolean) => {
    setOpen({ template, builtIn, fromLink, busy: true });
    try {
      const items = await planTemplate(template, templateHost());
      setOpen((o) => (o && o.template === template ? { ...o, items, busy: false } : o));
    } catch (e) {
      setOpen((o) => (o && o.template === template ? { ...o, error: e instanceof Error ? e.message : String(e), busy: false } : o));
    }
  };

  // A shared link opens the preview on its own: `/settings?template=…`
  useEffect(() => {
    const v = new URLSearchParams(location.search).get("template");
    if (!v) return;
    const r = readTemplateInput(v, builtIns);
    if ("error" in r) toast(tt("import_failed", { error: r.error }), "error");
    else void preview(r.template, r.builtIn, true);
    // drop the parameter so a reload does not ask again
    const u = new URL(location.href);
    u.searchParams.delete("template");
    history.replaceState(history.state, "", u.pathname + u.search + u.hash);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const apply = async () => {
    if (!open) return;
    const { template } = open;
    setOpen({ ...open, busy: true });
    const result = await applyTemplate(template, templateHost());
    const name = localized(template.name, lang);
    keepApplied(template, name);
    setApplied({ id: template.id, name });
    setOpen((o) => (o ? { ...o, result, busy: false } : o));
    const failed = result.filter((i) => i.status === "failed").length;
    toast(failed ? tt("applied_with_failures", { name, count: failed }) : tt("applied", { name }), failed ? "error" : "ok");
  };

  const share = (template: PersonaTemplate, builtIn: boolean) => void copyText(templateLink(location.origin, template, builtIn), { done: tt("link_copied") });

  const importIt = () => {
    const r = readTemplateInput(importText, builtIns);
    if ("error" in r) {
      setImportError(tt("import_failed", { error: r.error }));
      return;
    }
    setImportError("");
    void preview(r.template, r.builtIn, !r.builtIn);
  };

  const all = open?.result ?? open?.items;
  // what is already so folds into one line, so the list is what the template does
  const shown = all?.filter((i) => i.status !== "unchanged");
  const already = (all?.length ?? 0) - (shown?.length ?? 0);
  return (
    <Group title={tt("title")} anchor="templates" footer={tt("footer")}>
      {builtIns.map((b) => (
        <Row key={b.id} label={localized(b.name, lang)} hint={localized(b.description, lang)} wrapHint stackOnNarrow anchor={`template-${b.id}`}>
          <div className="flex shrink-0 gap-2">
            <Button tone="quiet" icon="share" onClick={() => share(b, true)} className="touch-hit whitespace-nowrap">{tt("share")}</Button>
            <Button onClick={() => void preview(b, true, false)} className="touch-hit whitespace-nowrap">{applied?.id === b.id ? tt("in_use") : tt("use")}</Button>
          </div>
        </Row>
      ))}
      {applied && !builtIns.some((b) => b.id === applied.id) && <Row label={tt("in_use_named", { name: applied.name })} />}
      <Row label={tt("import")} hint={importError || tt("import_hint")} wrapHint stackOnNarrow anchor="template-import">
        <div className="flex w-full min-w-0 gap-2 sm:w-auto">
          <input
            aria-label={tt("import")}
            value={importText}
            onChange={(e) => setImportText(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter") importIt(); }}
            placeholder={tt("import_placeholder")}
            className="min-w-0 flex-1 rounded-control bg-field px-3 py-1.5 text-meta text-ink shadow-hairline outline-none sm:w-56"
          />
          <Button disabled={!importText.trim()} onClick={importIt} className="touch-hit whitespace-nowrap">{tt("preview")}</Button>
        </div>
      </Row>

      <Dialog
        open={open !== null}
        onClose={() => setOpen(null)}
        title={open ? localized(open.template.name, lang) : ""}
        width={520}
        footer={open && (
          <div className="flex flex-wrap justify-end gap-2">
            <Button tone="quiet" onClick={() => setOpen(null)}>{open.result ? tt("done") : tt("cancel")}</Button>
            {!open.result && (
              <Button tone="primary" busy={open.busy} disabled={open.busy || !open.items?.some((i) => i.status === "change")} onClick={() => void apply()}>{tt("apply")}</Button>
            )}
          </div>
        )}
      >
        {open && (
          <div className="flex flex-col gap-3">
            {open.template.description && <p className="text-meta text-ink-2">{localized(open.template.description, lang)}</p>}
            {open.fromLink && <p className="rounded-control bg-inset px-3 py-2 text-meta text-ink-2" role="note">{tt("from_link")}</p>}
            {open.error && <p className="text-meta text-red" role="alert">{open.error}</p>}
            {open.busy && !shown && <p className="text-meta text-ink-3" role="status">{tt("checking")}</p>}
            {shown && shown.length === 0 && <p className="text-meta text-ink-3">{tt("nothing")}</p>}
            {shown && shown.length > 0 && (
              <ul className="flex flex-col divide-y divide-line rounded-control shadow-hairline" data-template-items>
                {shown.map((i, n) => (
                  <li key={n} className="flex items-start gap-3 px-3 py-2">
                    <div className="min-w-0 flex-1">
                      <div className="text-meta text-ink">{i.label}</div>
                      {i.detail && <div className="break-words text-submeta text-ink-3">{i.detail}</div>}
                    </div>
                    <span className={i.status === "failed" ? "shrink-0 text-submeta text-red" : i.status === "change" || i.status === "applied" ? "shrink-0 text-submeta font-medium text-ink" : "shrink-0 text-submeta text-ink-3"}>
                      {tt(`status_${i.status}`)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
            {shown && shown.length > 0 && already > 0 && <p className="text-submeta text-ink-3">{tt("already", { count: already })}</p>}
            {!open.result && open.items?.some((i) => i.part === "navigation" || i.part === "settings" || i.part === "extensions") && (
              <p className="text-submeta text-ink-3">{tt("scope_note")}</p>
            )}
          </div>
        )}
      </Dialog>
    </Group>
  );
}
