import { t } from "../../i18n";
import { BASE } from "../../lib/base";
import { useDeclaredList } from "../../lib/extension-declared";

export interface ExtensionWebShell {
  extension: string;
  extension_name: string;
  version: string;
  id: string;
  key: string;
  name: string;
  description?: string;
}

const COOKIE_NAME = "persona_web_shell";

export function useWebShells(): ExtensionWebShell[] {
  return useDeclaredList<ExtensionWebShell>("web-shells", "web_shells");
}

export function readWebShellCookie(raw = typeof document === "undefined" ? "" : document.cookie): string {
  for (const part of raw.split(";")) {
    const trimmed = part.trim();
    if (!trimmed.startsWith(`${COOKIE_NAME}=`)) continue;
    try {
      return decodeURIComponent(trimmed.slice(COOKIE_NAME.length + 1)).trim();
    } catch {
      return "";
    }
  }
  return "";
}

export function writeWebShellCookie(choice: string, base = BASE, secure = typeof location !== "undefined" && location.protocol === "https:"): void {
  if (typeof document === "undefined") return;
  const cookiePath = base ? `${base}/` : "/";
  const sec = secure ? "; Secure" : "";
  if (!choice) {
    document.cookie = `${COOKIE_NAME}=; Path=${cookiePath}; Max-Age=0; SameSite=Lax${sec}`;
    return;
  }
  // Keep "/" unescaped (valid RFC 6265 cookie-octet) so browsers send "<ext>/<id>" directly; server also decodes %2F.
  document.cookie = `${COOKIE_NAME}=${choice}; Path=${cookiePath}; Max-Age=31536000; SameSite=Lax${sec}`;
}

export function shellSwitchHref(choice: string, base = BASE): string {
  const root = base ? `${base}/` : "/";
  return choice ? root : `${root}?shell=builtin`;
}

/** Unregisters only Persona's own Service Worker registration (never unrelated apps on sibling subpaths of the same origin)
 *  and clears persona-* caches so same-path /assets/ never bleed across shells. */
export async function clearBuiltInShellCaches(base = BASE): Promise<void> {
  const tasks: Promise<unknown>[] = [];
  const expectedScope = base ? `${base.replace(/\/+$/, "")}/` : "/";
  if (typeof navigator !== "undefined" && navigator.serviceWorker?.getRegistrations) {
    tasks.push(
      navigator.serviceWorker
        .getRegistrations()
        .then((regs) =>
          Promise.allSettled(
            regs
              .filter((r) => {
                try {
                  return new URL(r.scope, typeof location !== "undefined" ? location.origin : "http://localhost").pathname === expectedScope;
                } catch {
                  return false;
                }
              })
              .map((r) => r.unregister()),
          ),
        )
        .catch(() => {}),
    );
  }
  if (typeof caches !== "undefined" && caches.keys) {
    tasks.push(
      caches.keys().then((names) => Promise.allSettled(names.filter((n) => n.startsWith("persona-")).map((n) => caches.delete(n)))).catch(() => {}),
    );
  }
  await Promise.allSettled(tasks);
}

/** Settings › Advanced › Appearance row for switching installed Web Shell extensions (#15396).
 *  Renders nothing when no web_shells extension is installed, keeping the default UI and first-screen budget untouched. */
export function WebShellPicker({
  onNavigate = (href: string) => window.location.assign(href),
  onClearCaches = clearBuiltInShellCaches,
}: {
  onNavigate?: (href: string) => void;
  onClearCaches?: () => Promise<void>;
} = {}) {
  const available = useWebShells();
  if (!available.length) return null;
  const active = readWebShellCookie();
  const current = available.some((s) => s.key === active) ? active : "";
  const handleSelect = async (choice: string) => {
    const valid = choice && available.some((s) => s.key === choice) ? choice : "";
    writeWebShellCookie(valid);
    await onClearCaches();
    onNavigate(shellSwitchHref(valid));
  };
  return (
    <div className="px-4 py-2.5" data-settings-row="web-shell">
      <div className="flex items-center gap-3">
        <span className="min-w-0 flex-1 text-sm text-ink">{t("settings-webshell.label")}</span>
        <select
          aria-label={t("settings-webshell.label")}
          value={current}
          onChange={(e) => void handleSelect(e.target.value)}
          className="h-8 max-w-[60%] rounded-control bg-field px-2.5 text-xs text-ink shadow-inset-field outline-none focus:shadow-[inset_0_0_0_1px_var(--accent)]"
        >
          <option value="">{t("settings-webshell.default")}</option>
          {available.map((s) => (
            <option key={s.key} value={s.key}>
              {s.name} · {s.extension_name}
            </option>
          ))}
        </select>
      </div>
      <p className="mt-1 text-submeta text-ink-3">{t("settings-webshell.hint")}</p>
    </div>
  );
}
