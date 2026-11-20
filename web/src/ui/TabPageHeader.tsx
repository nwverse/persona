import type { ReactNode } from "react";

/** The page edge above either kind of running tab. The terminal owns its own toolbar and native tab strip. */
export function TabPageHeader({ live, children }: { live?: boolean; children: ReactNode }) {
  return <header data-live-tab-header={live || undefined} className="@container flex h-14 shrink-0 items-center gap-2 border-b border-line bg-page/85 px-3 backdrop-blur-md sm:px-4">{children}</header>;
}

export function TabPageTitle({ computer, workspace, children }: { computer?: string; workspace?: string; children: ReactNode }) {
  if (computer === undefined) return children;
  const location = [computer, workspace].filter(Boolean).join(" · ");
  return (
    <div className="min-w-0 leading-tight">
      {children}
      {location && <div data-tab-location title={location} className="truncate text-submeta text-ink-3">{location}</div>}
    </div>
  );
}
