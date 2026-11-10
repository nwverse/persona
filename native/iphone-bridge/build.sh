#!/usr/bin/env bash
# Builds persona-iphone-bridge (docs/extensions/iphone-bridge.md). Not part of personad's build or CI: the node
# treats the binary as an optional external program, like the Cua Driver.
#
#   native/iphone-bridge/build.sh            test, lint and build a release binary
#   native/iphone-bridge/build.sh --install  also copy it to $PERSONA_HOME/bin (default ~/.persona/bin)
#
# Needs a Rust toolchain (rustup, cargo); without one it says so and stops.
set -euo pipefail

cd "$(dirname "$0")"
install=0
case "${1:-}" in
  "") ;;
  --install) install=1 ;;
  *) echo "usage: $0 [--install]" >&2; exit 2 ;;
esac

PATH="$HOME/.cargo/bin:$PATH"
if ! command -v cargo >/dev/null 2>&1; then
  echo "persona-iphone-bridge: FAILED: no Rust toolchain (install rustup, then rerun)" >&2
  exit 1
fi

cargo fmt --check
cargo clippy --locked --all-targets -- -D warnings
cargo test --locked --quiet
cargo build --locked --release --quiet

bin="$PWD/target/release/persona-iphone-bridge"
"$bin" version
if [ "$install" = 1 ]; then
  dest="${PERSONA_HOME:-$HOME/.persona}/bin"
  mkdir -p "$dest"
  install -m 0755 "$bin" "$dest/persona-iphone-bridge"
  echo "persona-iphone-bridge: OK: installed $dest/persona-iphone-bridge"
else
  echo "persona-iphone-bridge: OK: $bin"
fi
