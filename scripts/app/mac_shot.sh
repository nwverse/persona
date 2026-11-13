#!/bin/bash
# Screenshots of the Mac app, light and dark, without the screen (docs/apple/mac.md "Build, run, screenshot"): the app renders its own
# window into a PNG (`-macSnapshot`, DEBUG builds) and quits, so this works while the Mac is locked.
#
#   scripts/app/mac_shot.sh <name> [launch args…]
#   scripts/app/mac_shot.sh agents -tab dev
#   scripts/app/mac_shot.sh chat -openSession demo-mac/demo-1
#   BUILD=1 scripts/app/mac_shot.sh settings -tab settings     # build PersonaMac (Debug) first
#
# Writes $OUT/<name>-light.png and $OUT/<name>-dark.png (OUT defaults to /tmp/persona-mac-shots) and prints the paths.
# Always demo mode (`-demo.on YES`, made-up data, nothing leaves the Mac) unless DEMO=0 (then pass -pairURL for
# scripts/mock/mock_hub.py yourself). SIZE=1280x820, DELAY=4 (seconds before the shot), APP=<path to Persona.app>,
# APPEARANCES="light dark".
set -euo pipefail

name=${1:?usage: mac_shot.sh <name> [launch args…]}
shift || true
root=$(cd "$(dirname "$0")/../.." && pwd)
out=${OUT:-/tmp/persona-mac-shots}
size=${SIZE:-1280x820}
delay=${DELAY:-4}
derived=${DERIVED:-$root/../build/DerivedDataMac}
app=${APP:-$derived/Build/Products/Debug/Persona.app}

if [[ "${BUILD:-0}" == 1 ]]; then
    (
        cd "$root/apple"
        eval "$(python3 ../scripts/persona_config.py env)"
        python3 ../scripts/persona_config.py xcconfig >/dev/null
        xcodegen generate -q
        xcodebuild build -project Persona.xcodeproj -scheme PersonaMac -configuration Debug -destination "platform=macOS" \
            -derivedDataPath "$derived" CODE_SIGNING_ALLOWED=NO -quiet
    )
fi

bin="$app/Contents/MacOS/Persona"
[[ -x "$bin" ]] || { echo "mac_shot: no app at $app (BUILD=1 to build it, or APP=…)" >&2; exit 1; }
mkdir -p "$out"

demo=()
[[ "${DEMO:-1}" == 1 ]] && demo=(-demo.on YES)

status=0
for look in ${APPEARANCES:-light dark}; do
    png="$out/$name-$look.png"
    rm -f "$png"
    # The app quits on its own after the shot; the timeout only catches a hang (it then gets killed).
    "$bin" -ApplePersistenceIgnoreState YES -macAppearance "$look" \
        -macWindowSize "$size" -macSnapshot "$png" -macSnapshotDelay "$delay" -macSnapshotQuit YES \
        ${demo[@]+"${demo[@]}"} "$@" >/tmp/mac_shot.log 2>&1 &
    pid=$!
    limit=$(( ${delay%.*} + 30 ))
    for ((i = 0; i < limit * 2; i++)); do
        kill -0 "$pid" 2>/dev/null || break
        sleep 0.5
    done
    if kill -0 "$pid" 2>/dev/null; then
        kill "$pid" 2>/dev/null || true
        echo "mac_shot: $name ($look) timed out after ${limit}s, see /tmp/mac_shot.log" >&2
        status=1
        continue
    fi
    wait "$pid" 2>/dev/null || true
    if [[ -s "$png" ]]; then
        echo "$png"
    else
        echo "mac_shot: $name ($look) wrote nothing, see /tmp/mac_shot.log" >&2
        status=1
    fi
done
exit $status
