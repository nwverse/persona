#!/usr/bin/env bash
# Capture non-secret provenance for an owned Apple reference run (#6871).
# Usage: scripts/app/reference-metadata.sh <fresh-output-directory> [DerivedData]
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
OUT=${1:?new output directory required}
DD=${2:-}
[ ! -e "$OUT" ] || { printf 'reference-metadata: FAIL output already exists\n' >&2; exit 2; }
mkdir -p "$OUT"
eval "$(python3 "$ROOT/scripts/persona_config.py" env)"
{
  printf 'branch: '; git -C "$ROOT" branch --show-current
  printf 'source_sha: '; git -C "$ROOT" rev-parse HEAD
  printf 'source_dirty: '; if [ -n "$(git -C "$ROOT" status --short)" ]; then printf 'yes\n'; else printf 'no\n'; fi
  printf 'developer_dir: %s\n' "${DEVELOPER_DIR:-system default}"
  xcodebuild -version
  xcrun swift --version
  rustup run 1.98.0 rustc --version
  printf 'derived_data: %s\n' "${DD:-not supplied}"
  printf 'simulators:\n'
  xcrun simctl list devices booted
  printf 'xcode_26_verified: no\n'
  printf 'physical_device: not measured\n'
  printf 'store_download_size: not measured\n'
} > "$OUT/environment.txt"
xcrun simctl list devices available -j > "$OUT/simulators.json"
printf 'reference-metadata: ok %s\n' "$OUT/environment.txt"
