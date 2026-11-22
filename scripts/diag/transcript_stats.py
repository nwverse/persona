#!/usr/bin/env python3
"""Count what kinds of lines a machine's Claude Code transcripts and Codex rollouts hold.

    scripts/diag/transcript_stats.py [dir ...]

Default dirs: ~/.claude/projects, $CLAUDE_CONFIG_DIR/projects and ~/.codex/sessions. Run it after a Claude Code / Codex
upgrade: a type the parser (server/internal/transcript) does not know shows up here first. Prints counts only, no
content, and for each type one file:line to look at.
"""
import collections
import json
import os
import sys

from assistant_token_report import transcript_files


def files(dirs):
    paths = map(os.path.expanduser, dirs)
    return transcript_files(path for path in paths if os.path.isdir(path))


def main():
    dirs = sys.argv[1:] or [p for p in ("~/.claude/projects", os.path.join(os.environ.get("CLAUDE_CONFIG_DIR", "~/.claude"), "projects"),
                                        "~/.codex/sessions") if os.path.isdir(os.path.expanduser(p))]
    kinds, where = collections.Counter(), {}
    versions = collections.Counter()
    nfiles = nlines = 0
    for path in files(sorted(set(dirs))):
        nfiles += 1
        with open(path, errors="replace") as f:
            for i, line in enumerate(f, 1):
                nlines += 1
                try:
                    e = json.loads(line)
                except ValueError:
                    kinds["(bad json)"] += 1
                    continue
                if not isinstance(e, dict):
                    continue
                keys = []
                if "payload" in e:  # Codex rollout
                    p = e.get("payload") or {}
                    k = f"codex {e.get('type')}/{p.get('type') if isinstance(p, dict) else ''}"
                    if isinstance(p, dict) and p.get("type") == "item_completed":
                        k += "/" + str((p.get("item") or {}).get("type"))
                    if isinstance(p, dict) and p.get("cli_version"):
                        versions["codex " + p["cli_version"]] += 1
                    keys.append(k)
                else:
                    t, st = e.get("type"), e.get("subtype")
                    keys.append(f"claude {t}" + (f"/{st}" if st else ""))
                    if t == "attachment":
                        keys.append(f"claude attachment:{(e.get('attachment') or {}).get('type')}")
                    m = e.get("message")
                    if isinstance(m, dict) and isinstance(m.get("content"), list):
                        for b in m["content"]:
                            if isinstance(b, dict):
                                keys.append(f"claude {t}:{b.get('type')}")
                    if e.get("version"):
                        versions["claude " + e["version"]] += 1
                for k in keys:
                    kinds[k] += 1
                    where.setdefault(k, f"{path}:{i}")
    print(f"{nfiles} files, {nlines} lines")
    print("versions:", ", ".join(v for v, _ in versions.most_common(8)))
    for k, n in sorted(kinds.items()):
        print(f"{n:8d}  {k:60s} {where[k]}")


if __name__ == "__main__":
    main()
