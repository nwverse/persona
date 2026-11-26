// The file viewer as its own panel (a path from a tool step or a message; docs/apple/ios.md "File viewer"):
// shares the unified File Workbench (FilesPanel) with a collapsible directory tree, in-place file switching and back
// for folders and Markdown links.
import { below, dirName, fileName } from "../../lib/files";
import { navigate, routeAt } from "../../lib/router";
import { openPanel } from "../../lib/panel";
import { FilesPanel } from "./FilesPanel";

const normSlashes = (p: string) => p.replace(/\\/g, "/").replace(/\/+$/, "");

export function FileViewerPanel({ mid, sid, path, cwd, line, to, readOnly, onClose }: { mid: string; sid?: string; path: string; cwd?: string; line?: number; to?: number; readOnly?: boolean; onClose: () => void }) {
  const folder = dirName(path) || "/";
  const inCwd = Boolean(cwd && (below(cwd, path) !== null || normSlashes(path).startsWith(normSlashes(cwd) + "/")));
  const root = inCwd ? cwd! : folder;
  function showInFolder(file: string) {
    const folder = dirName(file) || "/";
    const route = routeAt(location.href);
    if (route?.name === "machine" && route.mid === mid && route.subview === "files") {
      onClose();
      navigate({ name: "machine", mid, subview: "files", path: folder });
    } else {
      openPanel({ kind: "files", mid, sid: sid ?? "", cwd: cwd || folder, path: folder, highlight: fileName(file) });
    }
  }
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
      onShowInFolder={readOnly ? undefined : showInFolder}
    />
  );
}
