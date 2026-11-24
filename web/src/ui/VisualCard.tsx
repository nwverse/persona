// Sandboxed interactive HTML visual card (```visual / ```visual-html / ```persona-visual / ```html-visual).
// Renders agent-generated HTML/SVG/CSS/JS inside a strict CSP iframe with live Persona theme tokens and
// ResizeObserver auto-sizing. While streaming, displays a placeholder and collapsible source preview.
import { useEffect, useId, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { t } from "../i18n";
import { copyText, Dialog, Spinner } from "./atoms";
import { Icon } from "./icons";

export const VISUAL_LANGUAGES = ["visual", "visual-html", "persona-visual", "html-visual"] as const;

export function isVisualLanguage(lang: string): boolean {
  return (VISUAL_LANGUAGES as readonly string[]).includes(lang);
}

const MIN_HEIGHT = 120;
const MAX_HEIGHT = 640;
const DEFAULT_HEIGHT = 280;

const VISUAL_CSP =
  "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; connect-src 'none'; frame-ancestors 'none'; form-action 'none'";

/** Whether the page is in dark mode now (the class views/theme.ts puts on <html>). */
function useDark(): boolean {
  return useSyncExternalStore(
    (on) => {
      const mo = new MutationObserver(on);
      mo.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
      return () => mo.disconnect();
    },
    () => document.documentElement.classList.contains("dark"),
    () => false,
  );
}

/** Extracts a plain-text title from an HTML `<title>` tag, if present. */
export function extractVisualTitle(html: string): string | undefined {
  const m = /<title[^>]*>([\s\S]*?)<\/title>/i.exec(html);
  const text = m?.[1]?.replace(/<[^>]*>/g, "").replace(/\s+/g, " ").trim();
  return text || undefined;
}

function themeCss(dark: boolean): string {
  const vars = dark
    ? [
        "color-scheme: dark;",
        "--bg: #17181a;",
        "--surface: #232427;",
        "--fg: #f2f3f4;",
        "--muted: #a5a8ad;",
        "--border: #2e3033;",
        "--accent: #3d9aff;",
        "--chart-1: #3987e5;",
        "--chart-2: #199e70;",
        "--chart-3: #9085e9;",
        "--chart-4: #d55181;",
      ]
    : [
        "color-scheme: light;",
        "--bg: #fafafb;",
        "--surface: #ffffff;",
        "--fg: #1f2124;",
        "--muted: #6e7177;",
        "--border: #ecedef;",
        "--accent: #0285ff;",
        "--chart-1: #0285ff;",
        "--chart-2: #1baf7a;",
        "--chart-3: #6250d6;",
        "--chart-4: #e87ba4;",
      ];
  return [
    `:root { ${vars.join(" ")} }`,
    `*, *::before, *::after { box-sizing: border-box; }`,
    `html, body { margin: 0; padding: 0; background: var(--surface); color: var(--fg); font-family: Inter, -apple-system, BlinkMacSystemFont, "SF Pro Text", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", ui-sans-serif, system-ui, sans-serif; -webkit-font-smoothing: antialiased; }`,
  ].join("\n");
}

function resizeBridgeScript(nonce: string): string {
  const safeNonce = JSON.stringify(nonce);
  return `(function(){var n=${safeNonce},last=0;function post(){var b=document.body;var h=b?Math.ceil(Math.max(b.scrollHeight,b.offsetHeight)):0;if(h>0&&h!==last){last=h;try{window.parent.postMessage({type:"persona-visual-height",nonce:n,height:h},"*");}catch(e){}}}if(typeof ResizeObserver!=="undefined"){var ro=new ResizeObserver(post);if(document.body)ro.observe(document.body);ro.observe(document.documentElement);}window.addEventListener("load",post);window.addEventListener("resize",post);if(document.readyState==="loading")document.addEventListener("DOMContentLoaded",post);else post();})();`;
}

/**
 * Wraps user/agent HTML inside a strict CSP document with live Persona theme tokens
 * and a ResizeObserver height-reporting bridge keyed by `nonce`.
 */
export function buildVisualSrcDoc(html: string, opts: { dark: boolean; nonce: string }): string {
  const cspMeta = `<meta http-equiv="Content-Security-Policy" content="${VISUAL_CSP}">`;
  const styleTag = `<style data-persona-visual-theme="">\n${themeCss(opts.dark)}\n</style>`;
  const bridgeTag = `<script data-persona-visual-resize="">${resizeBridgeScript(opts.nonce)}</script>`;
  const headInject = `${cspMeta}\n${styleTag}`;

  const hasFullDoc = /<!doctype\s+html|<html[\s>]|<head[\s>]|<body[\s>]/i.test(html);
  if (!hasFullDoc) {
    return [
      "<!doctype html>",
      "<html>",
      "<head>",
      '<meta charset="utf-8">',
      '<meta name="viewport" content="width=device-width, initial-scale=1">',
      headInject,
      "</head>",
      "<body>",
      html,
      bridgeTag,
      "</body>",
      "</html>",
    ].join("\n");
  }

  let doc = html;
  if (/<head[\s>]/i.test(doc)) {
    doc = doc.replace(/<head([^>]*)>/i, `<head$1>\n${headInject}`);
  } else if (/<html[\s>]/i.test(doc)) {
    doc = doc.replace(/<html([^>]*)>/i, `<html$1>\n<head>\n${headInject}\n</head>`);
  } else {
    doc = `${headInject}\n${doc}`;
  }

  if (/<\/body>/i.test(doc)) {
    doc = doc.replace(/<\/body>/i, `${bridgeTag}\n</body>`);
  } else {
    doc = `${doc}\n${bridgeTag}`;
  }

  return doc;
}

export function VisualCard({ code, title, streaming }: { code: string; title?: string; streaming?: boolean }) {
  const dark = useDark();
  const nonce = useId();
  const iframeRef = useRef<HTMLIFrameElement>(null);
  const [height, setHeight] = useState(DEFAULT_HEIGHT);
  const [showSource, setShowSource] = useState(false);
  const [copied, setCopied] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);
  const copyTimerRef = useRef<number | undefined>(undefined);

  const displayTitle = title?.trim() || extractVisualTitle(code) || t("ui-visualcard.default_title");
  const srcDoc = useMemo(() => (streaming ? "" : buildVisualSrcDoc(code, { dark, nonce })), [code, dark, nonce, streaming]);

  useEffect(() => () => window.clearTimeout(copyTimerRef.current), []);

  useEffect(() => {
    if (streaming) return;
    const onMessage = (e: MessageEvent) => {
      if (iframeRef.current && e.source && e.source !== iframeRef.current.contentWindow) return;
      const data = e.data as { type?: unknown; nonce?: unknown; height?: unknown } | null;
      if (!data || data.type !== "persona-visual-height" || data.nonce !== nonce) return;
      if (typeof data.height !== "number" || !Number.isFinite(data.height)) return;
      const clamped = Math.max(MIN_HEIGHT, Math.min(MAX_HEIGHT, Math.ceil(data.height)));
      setHeight(clamped);
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [nonce, streaming]);

  const handleCopy = () => {
    void copyText(code);
    setCopied(true);
    window.clearTimeout(copyTimerRef.current);
    copyTimerRef.current = window.setTimeout(() => setCopied(false), 1500);
  };

  if (streaming) {
    return (
      <div
        data-visual-card="streaming"
        className="my-3 overflow-hidden rounded-[10px] border border-line bg-surface shadow-hairline"
      >
        <div className="flex h-8 items-center justify-between gap-2 border-b border-line/70 bg-inset px-3 text-[11.5px] text-ink-3 select-none">
          <div className="flex min-w-0 items-center gap-1.5">
            <Icon name="sparkles" size={13} className="shrink-0 text-accent" />
            <span className="min-w-0 truncate font-medium text-ink-2">{displayTitle}</span>
          </div>
          <div className="flex shrink-0 items-center gap-1.5 text-[11px] text-ink-3">
            <Spinner size={11} />
            <span className="chat-shimmer">{t("ui-visualcard.generating")}</span>
          </div>
        </div>
        <div className="px-3 py-2.5 text-[12px] text-ink-3">
          <div className="flex items-center gap-2 text-ink-2">
            <span>{t("ui-visualcard.generating")}</span>
          </div>
          {code.trim() && (
            <details className="mt-2">
              <summary className="cursor-pointer text-[11.5px] text-ink-3 select-none hover:text-ink-2">
                {t("ui-visualcard.view_source_preview")}
              </summary>
              <pre className="!mt-1.5 !mb-0 max-h-48 overflow-auto !rounded-[6px] !bg-inset !px-2.5 !py-2 font-mono text-[11.5px] leading-relaxed text-ink-2">
                <code>{code}</code>
              </pre>
            </details>
          )}
        </div>
      </div>
    );
  }

  const btnBase =
    "touch-hit inline-flex h-6 shrink-0 items-center gap-1 whitespace-nowrap rounded-[6px] px-1.5 text-[11.5px] transition-colors duration-150";

  return (
    <div
      data-visual-card="ready"
      className="my-3 overflow-hidden rounded-[10px] border border-line bg-surface shadow-hairline"
    >
      <div className="flex h-8 items-center justify-between gap-2 border-b border-line/70 bg-inset px-3 text-[11.5px] text-ink-3 select-none">
        <div className="flex min-w-0 items-center gap-1.5">
          <Icon name="sparkles" size={13} className="shrink-0 text-accent" />
          <span className="min-w-0 truncate font-medium text-ink-2">{displayTitle}</span>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          <button
            type="button"
            aria-label={showSource ? t("ui-visualcard.preview") : t("ui-visualcard.source")}
            title={showSource ? t("ui-visualcard.switch_to_preview") : t("ui-visualcard.view_source")}
            aria-pressed={showSource}
            onClick={() => setShowSource((s) => !s)}
            className={`${btnBase} ${showSource ? "bg-hover text-ink" : "text-ink-3 hover:bg-hover hover:text-ink-2"}`}
          >
            <Icon name={showSource ? "sparkles" : "terminal"} size={12.5} />
            <span>{showSource ? t("ui-visualcard.preview") : t("ui-visualcard.source")}</span>
          </button>
          <button
            type="button"
            aria-label={t("ui-visualcard.copy_source")}
            title={t("ui-visualcard.copy_source")}
            onClick={handleCopy}
            className={`${btnBase} ${copied ? "text-green" : "text-ink-3 hover:bg-hover hover:text-ink-2"}`}
          >
            <Icon name={copied ? "check" : "copy"} size={12.5} stroke={copied ? 2.4 : 1.9} />
            <span>{copied ? t("ui-visualcard.copied") : t("ui-visualcard.copy")}</span>
          </button>
          <button
            type="button"
            aria-label={t("ui-visualcard.fullscreen")}
            title={t("ui-visualcard.view_fullscreen")}
            onClick={() => setFullscreen(true)}
            className={`${btnBase} text-ink-3 hover:bg-hover hover:text-ink-2`}
          >
            <Icon name="external" size={12.5} />
            <span>{t("ui-visualcard.fullscreen")}</span>
          </button>
        </div>
      </div>

      {showSource ? (
        <pre className="!m-0 max-h-[480px] overflow-auto !rounded-none !border-0 !bg-inset !px-3 !py-2.5 !shadow-none font-mono text-[12px] leading-relaxed text-ink">
          <code>{code}</code>
        </pre>
      ) : (
        <iframe
          ref={iframeRef}
          sandbox="allow-scripts"
          title={displayTitle}
          srcDoc={srcDoc}
          className="block w-full border-0 bg-surface"
          style={{ height }}
        />
      )}

      <Dialog open={fullscreen} onClose={() => setFullscreen(false)} title={displayTitle} width={1080}>
        <iframe
          sandbox="allow-scripts"
          title={t("ui-visualcard.fullscreen_title", { title: displayTitle })}
          srcDoc={srcDoc}
          className="block h-[75vh] w-full rounded-[8px] border border-line bg-surface"
        />
      </Dialog>
    </div>
  );
}
