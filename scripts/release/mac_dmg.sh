#!/bin/bash
# The Mac app (PersonaMac) in a drag-to-install disk image, build-release/dmg/Persona-<marketing version>.dmg: one
# fixed Finder window, the app on the left, an Applications link on the right and an arrow between them on a background
# drawn by dmg_background.swift; volume name Persona with the app's icon; compressed and read-only.
#
#   ./scripts/release/mac_dmg.sh [build-number]                  # ad hoc signed, NOT notarized: try it on this Mac only
#   DEVELOPER_ID=1 ./scripts/release/mac_dmg.sh [build-number]   # Developer ID, notarized and stapled: to hand out
#   ARCHIVE=<.xcarchive> ./scripts/release/mac_dmg.sh            # package an archive this script built, not a new build
#
# Developer ID needs the team's "Developer ID Application" identity in this Mac's keychain (only the Account Holder can
# create the certificate) and makes its profiles with asc_signing.py --developer-id; notarization uses the App Store
# Connect API key from the config. Nothing is published. docs/operations/release.md "Mac disk image".
set -euo pipefail
cd "$(dirname "$0")/../.."
umask 022  # every file in the image readable by every user (docs/operations/release.md "Mac version")
_env=$(python3 scripts/persona_config.py env) || exit $?
eval "$_env"; unset _env

OUT=build-release/dmg
WORK="$OUT/work"
# The window, in points from its top left. The background's arrow joins the two icons on row ROW.
WIDTH=660 HEIGHT=400 ICON=128 APP_X=165 LINK_X=495 ROW=180
ICONSET=apple/Persona/Resources/Assets.xcassets/AppIcon.appiconset
rm -rf "$WORK"
mkdir -p "$WORK"

if [ -n "${DEVELOPER_ID:-}" ]; then
  IDENTITY=$(security find-identity -v -p codesigning \
    | sed -nE "s/.*\"(Developer ID Application: .*\($PERSONA_TEAM_ID\))\".*/\1/p" | head -1)
  if [ -z "$IDENTITY" ]; then
    python3 scripts/release/asc_signing.py --developer-id --check >/dev/null || true
    echo "No \"Developer ID Application\" identity of team $PERSONA_TEAM_ID in this Mac's keychain. If the team has the" >&2
    echo "certificate but its private key is on another Mac, export it from there (.p12); otherwise see above." >&2
    exit 1
  fi
  # Recreated each time, as the App Store profiles are: cheap, and always in step with the App ID's capabilities.
  python3 scripts/release/asc_signing.py --developer-id
fi

if [ -z "${ARCHIVE:-}" ]; then
  python3 scripts/persona_config.py xcconfig
  echo "Building with $(xcodebuild -version | head -1) (config \"xcode\" picks another, docs/operations/config.md)"
  python3 scripts/privacy_check.py
  (cd apple && xcodegen generate -q)
  BUILD="${1:-$(date +%Y%m%d%H%M)}"
  # Kept where Xcode Organizer looks, so its dSYMs symbolicate crash reports from this build.
  ARCHIVE="$HOME/Library/Developer/Xcode/Archives/$(date +%Y-%m-%d)/Persona Mac $BUILD.xcarchive"
  # Unsigned, then each bundle signed ad hoc with its target's entitlements (entitlements.py): the build itself needs
  # no certificate, profile or registered Mac.
  xcodebuild archive -project apple/Persona.xcodeproj -scheme PersonaMac -configuration Release \
    -destination 'generic/platform=macOS' -archivePath "$ARCHIVE" -derivedDataPath build-release/DerivedDataMac \
    CURRENT_PROJECT_VERSION="$BUILD" CODE_SIGNING_ALLOWED=NO | grep -E "error:|ARCHIVE (SUCCEEDED|FAILED)"
  python3 scripts/release/entitlements.py sign "$ARCHIVE"
fi
BUILT="$ARCHIVE/Products/Applications/Persona.app"
[ -d "$BUILT" ] || { echo "no Persona.app in $ARCHIVE" >&2; exit 1; }
# The checks a TestFlight build gets: placeholder permission strings, and nothing of this person (hub address, host,
# email, path) in what anyone may download.
python3 scripts/release/check_bundle_strings.py "$BUILT"
python3 scripts/release/check_bundle_privacy.py "$BUILT"
VERSION=$(/usr/libexec/PlistBuddy -c "Print :CFBundleShortVersionString" "$BUILT/Contents/Info.plist")
DMG="$OUT/Persona-$VERSION.dmg"

notarize() {  # <zip or dmg>: submit, wait, and stop unless Apple accepts it (printing its log)
  local result id="" status=""
  for attempt in 1 2 3; do
    result=$(xcrun notarytool submit "$1" --key "$ASC_KEY_PATH" --key-id "$ASC_KEY_ID" --issuer "$ASC_ISSUER_ID" \
      --wait --timeout 1h --output-format json) || true
    id=$(python3 -c 'import json, sys; print(json.loads(sys.argv[1] or "{}").get("id", ""))' "$result" 2>/dev/null) || true
    status=$(python3 -c 'import json, sys; print(json.loads(sys.argv[1] or "{}").get("status", ""))' "$result" 2>/dev/null) || true
    # Submit again only when nothing reached Apple; a submission that was judged has its answer.
    [ -z "$id" ] || break
    echo "notarytool could not submit $(basename "$1") (attempt $attempt); retrying in $((attempt * 30))s" >&2
    sleep $((attempt * 30))
  done
  echo "notarization of $(basename "$1"): ${status:-no answer} ($id)"
  if [ "$status" != Accepted ]; then
    [ -z "$id" ] || xcrun notarytool log "$id" --key "$ASC_KEY_PATH" --key-id "$ASC_KEY_ID" --issuer "$ASC_ISSUER_ID" >&2
    exit 1
  fi
}

APP="$WORK/Persona.app"
if [ -n "${DEVELOPER_ID:-}" ]; then
  # The export signs every bundle with Developer ID, its profile's entitlements (push becomes production) and the
  # hardened runtime the targets set.
  python3 scripts/persona_config.py developer-id-export-options "$WORK/ExportOptions.plist"
  xcodebuild -exportArchive -archivePath "$ARCHIVE" -exportOptionsPlist "$WORK/ExportOptions.plist" \
    -exportPath "$WORK/export" | grep -E "error:|EXPORT (SUCCEEDED|FAILED)"
  ditto "$WORK/export/Persona.app" "$APP"
  codesign --verify --deep --strict "$APP"
  codesign -dv "$APP" 2>&1 | grep -q "flags=.*runtime" || { echo "no hardened runtime on $APP" >&2; exit 1; }
  python3 scripts/release/entitlements.py check "$APP"
  if ! codesign -d --entitlements - --xml "$APP" 2>/dev/null \
      | grep -q "<key>com.apple.developer.aps-environment</key><string>production</string>"; then
    echo "push (com.apple.developer.aps-environment) is not production in $APP" >&2; exit 1
  fi
  ditto -c -k --keepParent "$APP" "$WORK/Persona.zip"
  notarize "$WORK/Persona.zip"
  xcrun stapler staple "$APP"
else
  # Push, the shared keychain groups and time-sensitive notifications are granted only by a provisioning profile; an
  # ad hoc signature claiming them is killed at launch. Without them the app runs, minus remote push.
  ditto "$BUILT" "$APP"
  shopt -s nullglob
  for bundle in "$APP"/Contents/PlugIns/*.appex "$APP"; do
    codesign -d --entitlements - --xml "$bundle" 2>/dev/null > "$WORK/entitlements.plist"
    python3 - "$WORK/entitlements.plist" <<'EOF'
import plistlib, sys
with open(sys.argv[1], "rb") as f:
    data = f.read()
entitlements = plistlib.loads(data) if data.strip() else {}
with open(sys.argv[1], "wb") as f:
    plistlib.dump({k: v for k, v in entitlements.items() if k.startswith("com.apple.security.")}, f)
EOF
    codesign --force --sign - --options runtime --entitlements "$WORK/entitlements.plist" "$bundle"
  done
fi

# The background, 1x and 2x in one TIFF so Retina screens get the sharp one; the volume icon from the app icon's Mac sizes.
swift scripts/release/dmg_background.swift "$WORK/background" "$WIDTH" "$HEIGHT" "$APP_X" "$LINK_X" "$ROW"
tiffutil -cathidpicheck "$WORK/background/background.png" "$WORK/background/background@2x.png" \
  -out "$WORK/background.tiff" >/dev/null
mkdir "$WORK/Persona.iconset"
for size in 16 32 128 256 512; do
  cp "$ICONSET/mac-$size.png" "$WORK/Persona.iconset/icon_${size}x${size}.png"
  cp "$ICONSET/mac-$size@2x.png" "$WORK/Persona.iconset/icon_${size}x${size}@2x.png"
done
iconutil -c icns -o "$WORK/VolumeIcon.icns" "$WORK/Persona.iconset"

# dmgbuild writes the window's layout (.DS_Store) itself: scripting Finder needs an unlocked screen and an Automation
# grant, and hangs on a locked or remote release Mac.
VENV=build-release/dmgbuild
[ -x "$VENV/bin/python" ] || python3 -m venv "$VENV"
"$VENV/bin/python" -m pip install -q --disable-pip-version-check --no-deps --only-binary :all: --require-hashes \
  -r scripts/release/dmgbuild-requirements.txt
rm -f "$DMG"
WIDTH=$WIDTH HEIGHT=$HEIGHT ICON=$ICON APP_X=$APP_X LINK_X=$LINK_X ROW=$ROW \
  "$VENV/bin/python" - "$DMG" "$APP" "$WORK/background.tiff" "$WORK/VolumeIcon.icns" <<'EOF'
import os, sys
import dmgbuild

dmg, app, background, icon = sys.argv[1:]
n = lambda key: int(os.environ[key])
dmgbuild.build_dmg(dmg, "Persona", settings={
    "format": "UDZO", "compression_level": 9, "filesystem": "HFS+",
    "files": [(app, "Persona.app")],
    "symlinks": {"Applications": "/Applications"},
    # No hide_extensions: the Finder info it puts on the app breaks the app's signature check (Finder hides .app anyway).
    "icon": icon,  # .VolumeIcon.icns, with the volume's custom icon bit set
    "background": background,  # hidden: .background.tiff
    "window_rect": ((200, 300), (n("WIDTH"), n("HEIGHT"))),  # from the screen's bottom left
    "show_toolbar": False, "show_sidebar": False, "show_status_bar": False, "show_pathbar": False, "show_tab_view": False,
    "icon_size": n("ICON"), "text_size": 13,
    "icon_locations": {"Persona.app": (n("APP_X"), n("ROW")), "Applications": (n("LINK_X"), n("ROW"))},
}, lookForHiDPI=False)
EOF
hdiutil verify -quiet "$DMG"
if [ -n "${DEVELOPER_ID:-}" ]; then
  codesign --sign "$IDENTITY" --timestamp "$DMG"
  notarize "$DMG"
  xcrun stapler staple "$DMG"
  xcrun stapler validate "$DMG"
  spctl -a -vv -t open --context context:primary-signature "$DMG"
fi

# Read the finished image back: dmgbuild sets the volume icon with SetFile and carries on silently if that fails.
MOUNT=$(mktemp -d)
hdiutil attach -nobrowse -readonly -mountpoint "$MOUNT" "$DMG" >/dev/null
problems=""
codesign --verify --deep --strict "$MOUNT/Persona.app" || problems+=" app signature;"
[ "$(GetFileInfo -aC "$MOUNT")" = 1 ] && [ -f "$MOUNT/.VolumeIcon.icns" ] || problems+=" volume icon;"
[ "$(readlink "$MOUNT/Applications")" = /Applications ] || problems+=" Applications link;"
[ -f "$MOUNT/.background.tiff" ] && [ -f "$MOUNT/.DS_Store" ] || problems+=" window layout;"
if [ -n "${DEVELOPER_ID:-}" ]; then
  { spctl -a -vv -t exec "$MOUNT/Persona.app" && xcrun stapler validate "$MOUNT/Persona.app"; } || problems+=" Gatekeeper;"
fi
hdiutil detach -quiet "$MOUNT"
[ -z "$problems" ] || { echo "$DMG is not right:$problems" >&2; exit 1; }

if [ -n "${DEVELOPER_ID:-}" ]; then
  SIGNED="Developer ID, notarized and stapled"
else
  SIGNED="ad hoc, NOT notarized: Gatekeeper blocks it on other Macs; DEVELOPER_ID=1 for one to hand out"
fi
echo "$DMG ($(du -h "$DMG" | cut -f1 | tr -d ' '), sha256 $(shasum -a 256 "$DMG" | cut -d' ' -f1)): $SIGNED"
