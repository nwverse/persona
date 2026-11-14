#!/usr/bin/env bash
# Reproduce the local iOS payload comparison, never an App Store download measurement.
# Every xcodebuild goes through ~/.local/bin/xcode-slot. It builds no test runner, does not archive, sign for
# distribution, export, upload or modify the normal apple/project.yml / Persona.xcodeproj.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
OUT=${1:?fresh external output directory required}
CONFIGURATION=${CONFIGURATION:-Release}
[ ! -e "$OUT" ] || { printf 'apple-size-reference: FAIL output already exists: %s\n' "$OUT" >&2; exit 2; }
[ -x "$HOME/.local/bin/xcode-slot" ] || { printf 'apple-size-reference: FAIL xcode-slot required\n' >&2; exit 2; }
mkdir -p "$OUT"
"$HOME/.local/bin/xcode-slot" "$ROOT/native/app-core/scripts/build-apple.sh" > "$OUT/core-build.log" 2>&1
python3 -I "$ROOT/native/app-core/scripts/apple-no-core-spec.py" --root "$ROOT" --output-dir "$OUT/spec"
eval "$(python3 "$ROOT/scripts/persona_config.py" env)"
xcodegen generate -q --spec "$OUT/spec/project.yml" --project "$OUT/no-core" --project-root "$ROOT/apple"
ln -s "$ROOT/apple/Config.xcconfig" "$OUT/no-core/Config.xcconfig"
COMMON=(-scheme Persona -configuration "$CONFIGURATION" -destination generic/platform=iOS
  CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO CODE_SIGN_IDENTITY= DEVELOPMENT_TEAM=
  'SWIFT_ACTIVE_COMPILATION_CONDITIONS=$(inherited) PERF_SIGNPOST')
# PERF_SIGNPOST is a repository-supported performance build condition. It also satisfies the current source's
# screenProbe condition. Both variants use exactly the same setting; normal Release without it is not measured here.
"$HOME/.local/bin/xcode-slot" timeout 3600 xcodebuild build -project "$ROOT/apple/Persona.xcodeproj" \
  -derivedDataPath "$OUT/with-dd" "${COMMON[@]}" > "$OUT/with-build.log" 2>&1
"$HOME/.local/bin/xcode-slot" timeout 3600 xcodebuild build -project "$OUT/no-core/PersonaReferenceNoCore.xcodeproj" \
  -derivedDataPath "$OUT/without-dd" "${COMMON[@]}" > "$OUT/without-build.log" 2>&1
python3 -I "$ROOT/native/app-core/scripts/apple-package-report.py" \
  --with-core "$OUT/with-dd/Build/Products/$CONFIGURATION-iphoneos/Persona.app" \
  --without-core "$OUT/without-dd/Build/Products/$CONFIGURATION-iphoneos/Persona.app" \
  --xcframework "$ROOT/apple/Packages/PersonaCore/build/PersonaCoreC.xcframework" --output "$OUT/report"
grep -n -A4 -B4 -- '-lpersona_core' "$OUT/with-build.log" > "$OUT/report/link-command.txt"
"$ROOT/scripts/app/reference-metadata.sh" "$OUT/metadata"
printf 'apple-size-reference: ok local payload report %s; store download not measured\n' "$OUT/report/package-size.json"
