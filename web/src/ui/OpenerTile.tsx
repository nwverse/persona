// A conversation an extension opened, marked with that extension's own icon (#10358): the one its manifest brings,
// as Settings › Extensions draws it (ExtensionLogo), in the size and corner of the tile Lark's mark has (ChannelMark.tsx).
// Until the icon is in, and when the extension has none, is not installed (any more) or its icon fails, the origin's
// own mark stays (`children`). A lazy chunk of OriginMark's: the first screen keeps only the check for an opener.
import type { ReactNode } from "react";
import { useOpenerIcon } from "../lib/opener-icons";
import { ExtensionLogo } from "./ExtensionLogo";

export function OpenerTile({ id, manager, size, radius, children }: { id: string; manager?: string; size: number; radius: number; children: ReactNode }) {
  const icon = useOpenerIcon(id, manager);
  if (!icon) return children;
  return (
    <span data-opener={id} className="flex shrink-0" style={{ width: size, height: size, borderRadius: radius }}>
      <ExtensionLogo url={icon.url} manager={icon.manager} iconSize={size} fallback={children} className="flex size-full items-center justify-center rounded-[inherit]" />
    </span>
  );
}
