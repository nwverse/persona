#!/usr/bin/env python3
"""Reads the personal build / deploy config (docs/config.md) so no script hard-codes one person's setup.

The config lives outside the repository so clean worktrees (releases) see it too:
  $JARVIS_CONFIG, else ~/.config/jarvis/config.json   (template: config.example.json)

  python3 scripts/jarvis_config.py env              # shell exports: eval "$(python3 scripts/jarvis_config.py env)"
  python3 scripts/jarvis_config.py xcconfig         # writes apple/Config.xcconfig (bundle id, team) for XcodeGen / Xcode
  python3 scripts/jarvis_config.py export-options <out.plist> [upload|export]
  python3 scripts/jarvis_config.py get <dotted.key>  # one value, e.g. hub.url
"""
import json
import os
import plistlib
import shlex
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def path() -> str:
    for key in ("PERSONA_CONFIG", "JARVIS_CONFIG"):
        if key in os.environ:
            return os.path.expanduser(os.environ[key])
    current = os.path.expanduser("~/.config/persona/config.json")
    legacy = os.path.expanduser("~/.config/jarvis/config.json")
    return current if os.path.lexists(current) or not os.path.lexists(legacy) else legacy


def config_alias(p: str):
    current = os.path.expanduser("~/.config/persona")
    legacy = os.path.expanduser("~/.config/jarvis")
    parent = os.path.dirname(os.path.abspath(p))
    if parent not in (current, legacy):
        return
    alias = legacy if parent == current else current
    if not os.path.lexists(alias):
        try:
            os.symlink(os.path.basename(parent), alias)
        except OSError:
            pass


def load() -> dict:
    p = path()
    if not os.path.exists(p):
        sys.exit(f"missing {p}\nCopy config.example.json there and fill it in (docs/config.md).")
    with open(p) as f:
        cfg = json.load(f)
    config_alias(p)
    return cfg


def get(cfg: dict, key: str, default=None):
    cur = cfg
    for part in key.split("."):
        if not isinstance(cur, dict) or part not in cur:
            return default
        cur = cur[part]
    return cur


def machine(cfg: dict, mid: str) -> dict:
    for m in cfg.get("machines", []):
        if m.get("id") == mid:
            return m
    sys.exit(f"machine {mid!r} is not in {path()}")


def env(cfg: dict) -> dict:
    store = cfg.get("app_store", {})
    hub = cfg.get("hub", {})
    hub_ssh = machine(cfg, hub["machine"]).get("ssh", hub["machine"]) if hub.get("machine") else ""
    out = {
        "JARVIS_BUNDLE_ID": get(cfg, "app.bundle_id", ""),
        "JARVIS_TEAM_ID": get(cfg, "app.team_id", ""),
        "ASC_KEY_ID": store.get("key_id", ""),
        "ASC_ISSUER_ID": store.get("issuer_id", ""),
        "ASC_KEY_PATH": os.path.expanduser(store.get("key_path", "")),
        "ASC_APP_ID": str(store.get("app_id", "")),
        "JARVIS_HUB_MACHINE": hub.get("machine", ""),
        "JARVIS_HUB_SSH": hub_ssh,
        "JARVIS_HUB_URL": hub.get("url", ""),
        "JARVIS_MACHINES": " ".join(m["id"] for m in cfg.get("machines", [])),
        "JARVIS_SIM": cfg.get("simulator", ""),
        # Where scripts/release/publish.sh puts jarvisd and install.sh (the site/ worker's domain), and the wrangler to run.
        "JARVIS_PUBLISH_URL": get(cfg, "publish.site_url", "").rstrip("/"),
        "JARVIS_WRANGLER": os.path.expanduser(get(cfg, "publish.wrangler", "")),
        # Meta glasses (docs/meta-glasses.md): the project.yml include that adds the Meta Wearables SDK is on only for YES.
        "JARVIS_META_WEARABLES": "YES" if get(cfg, "app.meta_wearables.enabled", False) is True else "NO",
    }
    # Which Xcode builds (and releases) the app; a DEVELOPER_DIR already in the environment wins. iPhone Duo needs
    # Xcode 27 or later: built with an older one, the app does not reach under the status bar and the camera.
    if cfg.get("xcode") and not os.environ.get("DEVELOPER_DIR"):
        out["DEVELOPER_DIR"] = os.path.join(os.path.expanduser(cfg["xcode"]), "Contents", "Developer")
    for key, value in list(out.items()):
        if key.startswith("JARVIS_"):
            current = "PERSONA_" + key[len("JARVIS_"):]
            out[current] = out[key] = value
    return out


def app_group(cfg: dict) -> str:
    return get(cfg, "app.app_group", "") or "group." + get(cfg, "app.bundle_id", "")


# The app's entitlements. The CarPlay voice entitlement needs Apple's approval first (docs/carplay.md); the generated copy
# sits in apple/, outside the Persona/ source folder, so XcodeGen does not pick it up as a resource.
APP_ENTITLEMENTS = os.path.join("Persona", "Resources", "Persona.entitlements")
CARPLAY_ENTITLEMENTS = "PersonaCarPlay.entitlements"


def app_entitlements(cfg: dict) -> str:
    # Opt-in: an App ID without the approved entitlement cannot sign it, so signing without it must keep working.
    # Returns the path relative to apple/. Only JSON true turns it on: a string like "false" is truthy in Python.
    if get(cfg, "app.carplay_voice", False) is not True:
        return APP_ENTITLEMENTS
    with open(os.path.join(ROOT, "apple", APP_ENTITLEMENTS), "rb") as f:
        entitlements = plistlib.load(f)
    entitlements["com.apple.developer.carplay-voice-based-conversation"] = True
    with open(os.path.join(ROOT, "apple", CARPLAY_ENTITLEMENTS), "wb") as f:
        plistlib.dump(entitlements, f)
    return CARPLAY_ENTITLEMENTS


def xcconfig(cfg: dict):
    # Generated, git-ignored: the committed project only says $(JARVIS_BUNDLE_ID).
    out = os.path.join(ROOT, "apple", "Config.xcconfig")
    with open(out, "w") as f:
        f.write("// Generated from %s by scripts/jarvis_config.py; do not edit or commit.\n" % path())
        f.write("JARVIS_BUNDLE_ID = %s\n" % get(cfg, "app.bundle_id", ""))
        # The App Group the app shares files with the widget extension (docs/release.md "App Group").
        f.write("JARVIS_APP_GROUP = %s\n" % app_group(cfg))
        f.write("PERSONA_BUNDLE_ID = $(JARVIS_BUNDLE_ID)\n")
        f.write("PERSONA_APP_GROUP = $(JARVIS_APP_GROUP)\n")
        f.write("PERSONA_PUSH_RELAY = $(JARVIS_PUSH_RELAY)\n")
        f.write("DEVELOPMENT_TEAM = %s\n" % get(cfg, "app.team_id", ""))
        # The push relay the app registers with (docs/push-relay.md); "//" starts a comment in xcconfig, hence $().
        f.write("JARVIS_PUSH_RELAY = %s\n" % get(cfg, "app.push_relay", "").rstrip("/").replace("//", "/$()/"))
        # Which entitlements the app signs with (app_entitlements; docs/carplay.md).
        f.write("PERSONA_APP_ENTITLEMENTS = %s\n" % app_entitlements(cfg))
        # "No hub yet?" on the first screen opens this page (docs/getting-started.md published; empty: the row is hidden).
        f.write("PERSONA_GETTING_STARTED_URL = %s\n" % get(cfg, "publish.getting_started_url", "").replace("//", "/$()/"))
        # Meta Wearables (docs/meta-glasses.md): 0 means Developer Mode (no attestation), as Meta's docs say.
        f.write("PERSONA_META_APP_ID = %s\n" % (str(get(cfg, "app.meta_wearables.app_id", "") or "") or "0"))
        f.write("PERSONA_META_CLIENT_TOKEN = %s\n" % (get(cfg, "app.meta_wearables.client_token", "") or ""))
        # The in-app tunnel core (docs/network.md"Connect to the hub through an in-app tunnel"): linked into device builds only when set.
        include, ldflags = tunnel_core(cfg)
        f.write("PERSONA_TUNNEL_INCLUDE = %s\n" % include)
        f.write("PERSONA_TUNNEL_LDFLAGS = %s\n" % ldflags)


def tunnel_core(cfg: dict) -> tuple[str, str]:
    """`app.tunnel_core`: a directory with libpersonatunnel.a and the module.modulemap of PersonaTunnelCore."""
    raw = get(cfg, "app.tunnel_core", "") or ""
    if not raw:
        return "", ""
    d = os.path.abspath(os.path.expanduser(raw))
    missing = [n for n in ("libpersonatunnel.a", "module.modulemap") if not os.path.isfile(os.path.join(d, n))]
    if missing:
        sys.exit(f"app.tunnel_core {d}: missing {', '.join(missing)} (docs/network.md「经 App 内隧道连 hub」)")
    q = '"%s"' % d
    return q, f"-L{q} -lpersonatunnel -lresolv -framework Security -framework CoreFoundation"


def export_options(cfg: dict, out: str, destination: str = "upload"):
    bid = get(cfg, "app.bundle_id", "")
    plist = f"""<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>method</key>
	<string>app-store-connect</string>
	<key>destination</key>
	<string>{destination}</string>
	<key>signingStyle</key>
	<string>manual</string>
	<key>signingCertificate</key>
	<string>Apple Distribution</string>
	<key>provisioningProfiles</key>
	<dict>
		<key>{bid}</key>
		<string>Jarvis App Store</string>
		<key>{bid}.watchkitapp</key>
		<string>Jarvis Watch App Store</string>
		<key>{bid}.liveactivity</key>
		<string>Jarvis Live Activity App Store</string>
		<key>{bid}.notify</key>
		<string>Jarvis Notify App Store</string>
		<key>{bid}.share</key>
		<string>Persona Share App Store</string>
	</dict>
	<key>teamID</key>
	<string>{get(cfg, "app.team_id", "")}</string>
	<key>uploadSymbols</key>
	<true/>
	<key>manageAppVersionAndBuildNumber</key>
	<false/>
</dict>
</plist>
"""
    with open(out, "w") as f:
        f.write(plist)


def main():
    args = sys.argv[1:]
    cfg = load()
    if args[:1] == ["env"]:
        generated = env(cfg)
        for k, v in generated.items():
            print(f"export {k}={shlex.quote(v)}")
        for key in sorted(os.environ):
            if key.startswith("PERSONA_") and key not in generated and key.isascii() and key.replace("_", "").isalnum():
                legacy = "JARVIS_" + key[len("PERSONA_"):]
                print(f'export {legacy}="${{{key}}}"')
    elif args[:1] == ["xcconfig"]:
        xcconfig(cfg)
    elif args[:1] == ["export-options"] and len(args) >= 2:
        export_options(cfg, args[1], args[2] if len(args) > 2 else "upload")
    elif args[:1] == ["get"] and len(args) == 2:
        v = get(cfg, args[1])
        if v is None:
            sys.exit(f"{args[1]} is not set in {path()}")
        print(v if isinstance(v, str) else json.dumps(v))
    elif args[:1] == ["machine"] and len(args) == 2:
        print(json.dumps(machine(cfg, args[1])))
    else:
        sys.exit(__doc__)


if __name__ == "__main__":
    main()
