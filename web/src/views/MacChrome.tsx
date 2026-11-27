import { useLayoutEffect, useState } from "react";
import { t } from "../i18n";
import { setSidebarCollapsed, toggleMaximized } from "../lib/layout";
import { appleG2Contour } from "../lib/mac-chrome";
import { jumpHint, useBindings } from "../lib/shortcuts";
import { cx } from "../ui/atoms";
import { Icon } from "../ui/icons";
import { CollapseButton, SIDEBAR_CONTROL } from "./SidebarRail";

/** Controls parked inside the 44 px Apple G2 bay beside the macOS traffic lights across expanded, rail, and hidden sidebar states. */
export function MacBayTools({ bay, onSearch }: { bay: "expanded" | "rail" | "hidden"; onSearch: () => void }) {
  const bindings = useBindings();
  const collapsed = bay !== "expanded";
  return (
    <div
      data-mac-bay-tools
      className="pointer-events-auto absolute z-50 flex h-7 items-center gap-0.5 max-lg:hidden"
      style={{ left: 80, top: 11 }}
    >
      <span aria-hidden className="mr-1 h-3.5 w-px bg-line" />
      <CollapseButton
        collapsed={collapsed}
        chord={bindings["left-sidebar"]}
        size="sm"
        onToggle={bay === "hidden" ? () => {
          setSidebarCollapsed(false);
          toggleMaximized();
        } : undefined}
      />
      {collapsed && (
        <button
          type="button"
          onClick={onSearch}
          aria-label={t("sidebar.search_sessions")}
          title={`${t("sidebar.search_sessions")} (${jumpHint(bindings)})`}
          className={cx(SIDEBAR_CONTROL, "size-7")}
        >
          <Icon name="search" size={14} stroke={1.85} />
        </button>
      )}
    </div>
  );
}

function applyMainClip(el: HTMLElement, value: string): void {
  el.style.clipPath = value;
  el.style.setProperty("-webkit-clip-path", value);
}

/** Clips `<main>` with the 44 px Apple G2 continuous-curvature path and strokes its crisp 1 px border + top specular rim. */
export function MacMainContour({ mainEl, bay }: { mainEl: HTMLElement | null; bay: "expanded" | "rail" | "hidden" | null | undefined }) {
  const [size, setSize] = useState<{ w: number; h: number }>({ w: 0, h: 0 });
  const cutout = bay && bay !== "expanded" ? bay : null;

  useLayoutEffect(() => {
    if (!mainEl) return;
    if (!cutout) {
      applyMainClip(mainEl, "");
      return;
    }
    const measure = () => {
      const w = Math.round(mainEl.clientWidth);
      const h = Math.round(mainEl.clientHeight);
      if (w >= 100 && h >= 100) {
        const { clip } = appleG2Contour(w, h, cutout);
        applyMainClip(mainEl, `path("${clip}")`);
        setSize((prev) => (prev.w === w && prev.h === h ? prev : { w, h }));
      }
    };
    measure();
    if (typeof ResizeObserver === "undefined") return () => applyMainClip(mainEl, "");
    const ro = new ResizeObserver(measure);
    ro.observe(mainEl);
    return () => {
      ro.disconnect();
      applyMainClip(mainEl, "");
    };
  }, [mainEl, cutout]);

  if (!cutout || size.w < 100 || size.h < 100) return null;
  const { stroke, rim } = appleG2Contour(size.w, size.h, cutout);
  return (
    <svg
      aria-hidden
      data-mac-contour
      viewBox={`0 0 ${size.w} ${size.h}`}
      className="pointer-events-none absolute inset-0 z-30 h-full w-full max-lg:hidden"
    >
      <path d={stroke} fill="none" stroke="var(--line)" strokeWidth={1} />
      <path d={rim} fill="none" stroke="var(--mac-rim-top, transparent)" strokeWidth={1} />
    </svg>
  );
}
