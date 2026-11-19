#!/usr/bin/env bash
# Build the PersonaCore XCFramework from the checked-in Rust core. No binary is committed: the package's XCFramework
# and native/app-core/target are ignored. The simulator slice follows this Mac's architecture.
#
#   native/app-core/scripts/build-apple.sh               # device, simulator and macOS: release, CI, `swift test`
#   native/app-core/scripts/build-apple.sh --simulator   # the simulator slice only (scripts/app/sim.sh)
#
# The default builds all three slices, so both the iOS XCTest host and macOS `swift test` exercise the same C ABI.
# Built frameworks are kept in a cache shared by every worktree on the machine (#10713), keyed by a hash of the
# inputs (sources, Cargo files, toolchain, this script, the Mac's architecture, the Apple SDKs and the caller's
# RUSTFLAGS and deployment targets), never by the checkout's path: a worktree at the same Rust inputs copies the
# framework instead of building it. A full entry also serves a --simulator request. PERSONA_APP_CORE_CACHE moves
# the cache (default ~/Library/Caches/persona/app-core), PERSONA_APP_CORE_CACHE=0 turns it off, and only the newest
# PERSONA_APP_CORE_CACHE_KEEP keys (default 3) are kept.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
CORE="$ROOT/native/app-core"
SCRIPT="$CORE/scripts/build-apple.sh"
PACKAGE="$ROOT/apple/Packages/PersonaCore"
DEST="$PACKAGE/build/PersonaCoreC.xcframework"
STAMP="$PACKAGE/build/Inputs.stamp"
fail() { printf 'persona-core-apple: FAIL %s\n' "$*" >&2; exit 1; }
SLICES=all
for arg in "$@"; do
  case "$arg" in
    --simulator) SLICES=simulator ;;
    *) printf 'usage: %s [--simulator]\n' "$0" >&2; exit 2 ;;
  esac
done
TOOLCHAIN=$(sed -n 's/^channel = "\(.*\)"$/\1/p' "$CORE/rust-toolchain.toml")
[ -n "$TOOLCHAIN" ] || fail "missing pinned Rust toolchain"
command -v xcodebuild >/dev/null || fail "Xcode is required"
case "$(uname -m)" in
  arm64) SIM_TARGET=aarch64-apple-ios-sim ;;
  x86_64) SIM_TARGET=x86_64-apple-ios ;;
  *) fail "unsupported Mac architecture $(uname -m)" ;;
esac
CLEANUP=()
cleanup() {
  local path
  for path in ${CLEANUP[@]+"${CLEANUP[@]}"}; do rm -rf "$path"; done
}
trap cleanup EXIT

# The key: every input's path relative to the checkout and its content, then what the environment changes about the
# output. Never an absolute path, or no two worktrees would share an entry.
KEY=$(
  {
    find "$CORE/crates/persona-core" "$CORE/crates/persona-core-runtime" "$CORE/crates/persona-core-ffi" -type f -print
    printf '%s\n' "$CORE/Cargo.lock" "$CORE/Cargo.toml" "$CORE/rust-toolchain.toml" "$SCRIPT"
  } | LC_ALL=C sort | while IFS= read -r source; do
    printf '%s\0' "${source#"$ROOT/"}"
    shasum -a 256 < "$source"
  done
  printf 'arch %s\n' "$(uname -m)"
  for sdk in iphoneos iphonesimulator macosx; do
    printf 'sdk %s %s\n' "$sdk" "$(xcrun --sdk "$sdk" --show-sdk-build-version 2>/dev/null || echo none)"
  done
  printf 'RUSTFLAGS %s\nIPHONEOS_DEPLOYMENT_TARGET %s\nMACOSX_DEPLOYMENT_TARGET %s\n' \
    "${RUSTFLAGS:-}" "${IPHONEOS_DEPLOYMENT_TARGET:-}" "${MACOSX_DEPLOYMENT_TARGET:-}"
)
KEY=$(printf '%s' "$KEY" | shasum -a 256 | cut -c1-64)
mkdir -p "$PACKAGE/build"

# The worktree's own copy: current when it was made from this key and has the slices asked for.
if [ -d "$DEST" ] && [ -f "$STAMP" ]; then
  read -r have_key have_slices < "$STAMP" || true
  if [ "${have_key:-}" = "$KEY" ] && { [ "${have_slices:-}" = all ] || [ "${have_slices:-}" = "$SLICES" ]; }; then
    printf 'persona-core-apple: XCFramework is current (%s)\n' "${have_slices:-}"
    exit 0
  fi
fi

# Copies the framework at $1 to $2: a clone where the file system can (APFS, one volume), else a plain copy.
copy_framework() {
  cp -cR "$1" "$2" 2>/dev/null || { rm -rf "$2"; cp -R "$1" "$2"; }
}
# Puts the framework at $1 (moved: copy it first if it must stay) in the worktree and records what it is ($2: its slices).
install_framework() {
  rm -f "$STAMP"
  rm -rf "$DEST"
  mv "$1" "$DEST"
  printf '%s %s\n' "$KEY" "$2" > "$STAMP"
}

CACHE="${PERSONA_APP_CORE_CACHE:-}"
if [ -z "$CACHE" ]; then
  if [ "$(uname -s)" = Darwin ]; then CACHE="$HOME/Library/Caches/persona/app-core"; else CACHE="${XDG_CACHE_HOME:-$HOME/.cache}/persona/app-core"; fi
elif [ "$CACHE" = 0 ]; then
  CACHE=
fi
KEEP="${PERSONA_APP_CORE_CACHE_KEEP:-3}"
case "$KEEP" in ''|*[!0-9]*|0) fail "PERSONA_APP_CORE_CACHE_KEEP must be a positive number" ;; esac
ENTRY=${CACHE:+$CACHE/$KEY}
if [ -n "$ENTRY" ]; then
  # A --simulator request takes its own entry first (a third of the copy), else a full one.
  candidates=all
  [ "$SLICES" = simulator ] && candidates="simulator all"
  for slices in $candidates; do
    if [ -f "$ENTRY/$slices/PersonaCoreC.xcframework/Info.plist" ]; then
      touch "$ENTRY"
      next="$PACKAGE/build/.PersonaCoreC.xcframework.$$"
      CLEANUP+=("$next")
      rm -rf "$next"
      copy_framework "$ENTRY/$slices/PersonaCoreC.xcframework" "$next"
      install_framework "$next" "$slices"
      printf 'persona-core-apple: XCFramework (%s) from the shared cache %s\n' "$slices" "$ENTRY"
      exit 0
    fi
  done
fi

command -v rustup >/dev/null || fail "rustup is required"
rustup run "$TOOLCHAIN" cargo --version >/dev/null 2>&1 ||
  rustup toolchain install "$TOOLCHAIN" --profile minimal --component rustfmt,clippy
if [ "$SLICES" = simulator ]; then
  TARGETS="$SIM_TARGET"
else
  TARGETS="aarch64-apple-ios $SIM_TARGET $(rustc +"$TOOLCHAIN" -vV | sed -n 's/^host: //p')"
fi
# shellcheck disable=SC2086
rustup target add --toolchain "$TOOLCHAIN" $TARGETS
export CARGO_TARGET_DIR="${CARGO_TARGET_DIR:-$CORE/target/apple}"
# Panic metadata from dependencies names their registry source paths. Keep the build machine's home and checkout
# paths out of the published XCFramework, including when a caller supplies its own CARGO_HOME.
export RUSTFLAGS="${RUSTFLAGS:+$RUSTFLAGS }--remap-path-prefix=$HOME=/build/home --remap-path-prefix=$ROOT=/build/persona"
for TARGET in $TARGETS; do
  printf 'persona-core-apple: building %s\n' "$TARGET"
  SDKROOT=$(xcrun --sdk macosx --show-sdk-path)
  case "$TARGET" in
    aarch64-apple-ios) SDKROOT=$(xcrun --sdk iphoneos --show-sdk-path) ;;
    *-apple-ios*) SDKROOT=$(xcrun --sdk iphonesimulator --show-sdk-path) ;;
  esac
  export SDKROOT
  cargo +"$TOOLCHAIN" build --manifest-path "$CORE/Cargo.toml" --locked --release -p persona-core-ffi --target "$TARGET"
done
HEADERS=$(mktemp -d "${TMPDIR:-/tmp}/persona-core-apple-headers.XXXXXX")
CLEANUP+=("$HEADERS")
cp "$CORE/crates/persona-core-ffi/include/persona_core.h" "$HEADERS/persona_core.h"
cat > "$HEADERS/module.modulemap" <<'EOF'
module PersonaCoreC {
    header "persona_core.h"
    export *
}
EOF
ARGS=()
for TARGET in $TARGETS; do
  ARGS+=(-library "$CARGO_TARGET_DIR/$TARGET/release/libpersona_core.a" -headers "$HEADERS")
done
BUILT="$PACKAGE/build/.PersonaCoreC.built.$$"
CLEANUP+=("$BUILT")
rm -rf "$BUILT"
mkdir -p "$BUILT"
xcodebuild -create-xcframework "${ARGS[@]}" -output "$BUILT/PersonaCoreC.xcframework"

if [ -n "$ENTRY" ]; then
  # Into the cache under a private name, then renamed into place: a reader never sees half a framework, and when
  # another worktree published the same key meanwhile, its entry stays and this copy goes.
  mkdir -p "$ENTRY"
  staged="$ENTRY/.$SLICES.$$"
  CLEANUP+=("$staged")
  rm -rf "$staged"
  mkdir -p "$staged"
  copy_framework "$BUILT/PersonaCoreC.xcframework" "$staged/PersonaCoreC.xcframework"
  if [ ! -e "$ENTRY/$SLICES" ]; then
    mv "$staged" "$ENTRY/$SLICES"
    # Lost a race after the check: mv put this copy inside the winner's entry.
    rm -rf "$ENTRY/$SLICES/.$SLICES.$$"
  fi
  touch "$ENTRY"
  # Only the newest keys stay; this one was just touched. Anything else in the folder is left alone.
  # shellcheck disable=SC2012
  ls -1t "$CACHE" | { grep -E '^[0-9a-f]{64}$' || true; } | tail -n +"$((KEEP + 1))" | while IFS= read -r old; do
    rm -rf "${CACHE:?}/$old"
  done
fi
install_framework "$BUILT/PersonaCoreC.xcframework" "$SLICES"
printf 'persona-core-apple: XCFramework (%s) ready at %s\n' "$SLICES" "$DEST"
