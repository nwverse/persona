#!/usr/bin/env bash
# Builds persona-iphone-bridge (docs/extensions/iphone-bridge.md). Not part of personad's build or CI: the node
# treats the binary as an optional external program, like the Cua Driver.
#
#   native/iphone-bridge/build.sh               test, lint and build a release binary
#   native/iphone-bridge/build.sh --sim-runner  also build the simulator test runner (sim-runner/) next to it
#   native/iphone-bridge/build.sh --install     also copy them to $PERSONA_HOME/bin (default ~/.persona/bin)
#
# Needs a Rust toolchain (rustup, cargo); the test runner also needs Xcode and XcodeGen. Without them it
# says so and stops. The runner is built for the simulator only, without signing or a team.
set -euo pipefail

cd "$(dirname "$0")"
install=0
sim_runner=0
for arg in "$@"; do
  case "$arg" in
    --install) install=1 ;;
    --sim-runner) sim_runner=1 ;;
    *) echo "usage: $0 [--sim-runner] [--install]" >&2; exit 2 ;;
  esac
done

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

# The test runner goes next to the binary as persona-iphone-sim-runner/, where the bridge looks for it.
runner_out="$PWD/target/release/persona-iphone-sim-runner"
if [ "$sim_runner" = 1 ]; then
  for tool in xcodegen xcrun; do
    if ! command -v "$tool" >/dev/null 2>&1; then
      echo "persona-iphone-bridge: FAILED: the simulator test runner needs $tool" >&2
      exit 1
    fi
  done
  work="$PWD/target/sim-runner"
  rm -rf "$work" "$runner_out"
  mkdir -p "$work"
  cp -R sim-runner "$work/src"
  (cd "$work/src" && xcodegen generate --quiet)
  xcrun xcodebuild build-for-testing -quiet \
    -project "$work/src/PersonaSimRunner.xcodeproj" -scheme PersonaSimRunner \
    -destination 'generic/platform=iOS Simulator' -derivedDataPath "$work/derived"
  mkdir -p "$runner_out"
  cp -R "$work/derived/Build/Products/." "$runner_out/"
  ls "$runner_out"/*.xctestrun >/dev/null
fi

if [ "$install" = 1 ]; then
  dest="${PERSONA_HOME:-$HOME/.persona}/bin"
  mkdir -p "$dest"
  install -m 0755 "$bin" "$dest/persona-iphone-bridge"
  if [ -d "$runner_out" ]; then
    rm -rf "$dest/persona-iphone-sim-runner"
    cp -R "$runner_out" "$dest/persona-iphone-sim-runner"
  fi
  echo "persona-iphone-bridge: OK: installed $dest/persona-iphone-bridge"
else
  echo "persona-iphone-bridge: OK: $bin"
fi
