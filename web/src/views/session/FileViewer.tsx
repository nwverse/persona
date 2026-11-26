// The file viewer as its own panel (a path from a tool step or a message; docs/apple/ios.md "File viewer"):
// shares the unified File Workbench (FilesPanel) with a collapsible directory tree and in-place file switching.
import { below, dirName, fileName } from "../../lib/files";
import { openPanel } from "../../lib/panel";
import { FilesPanel } from "./FilesPanel";

const normSlashes = (p: string) => p.replace(/\\/g, "/").replace(/\/+$/, "");

export function FileViewerPanel({ mid, sid, path, cwd, line, to, readOnly, onClose }: { mid: string; sid?: string; path: string; cwd?: string; line?: number; to?: number; readOnly?: boolean; onClose: () => void }) {
  const folder = dirName(path) || "/";
  const inCwd = Boolean(cwd && (below(cwd, path) !== null || normSlashes(path).startsWith(normSlashes(cwd) + "/")));
  const root = inCwd ? cwd! : folder;
  return (
    <FilesPanel
      mid={mid}
      sid={sid ?? ""}
      cwd={cwd || folder}
      path={root}
      file={path}
      line={line}
      to={to}
      onClose={onClose}
      // Show in Files: the Files panel at its folder, the file marked (#5827). Omitted in read-only mode so it cannot escape to writable file management.
      onShowInFolder={readOnly ? undefined : (p) => openPanel({ kind: "files", mid, sid: sid ?? "", cwd: cwd || dirName(p) || "/", path: dirName(p) || "/", highlight: fileName(p) })}
    />
  );
}
