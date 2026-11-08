#!/usr/bin/env python3
"""Prepares App Store signing through the App Store Connect API (idempotent).

- registers the iPhone, Apple Watch, Live Activity and notification service extension bundle ids,
- enables PUSH_NOTIFICATIONS, USERNOTIFICATIONS_TIMESENSITIVE and HEALTHKIT on the iPhone App ID, APP_GROUPS on it and
  the Live Activity's,
- creates (or rebuilds) the "Persona App Store" / "Persona Watch App Store" / "Persona Live Activity App Store" /
  "Persona Notify App Store" profiles
  with the team's distribution certificate and installs them where Xcode looks.

The API key's role (App Manager) can't use cloud-managed signing, so release builds sign manually with
the "Apple Distribution" identity in the login keychain (see docs/operations/release.md).

  python3 scripts/release/asc_signing.py                 # App Store profiles
  python3 scripts/release/asc_signing.py --dev           # also development profiles for registered devices
  python3 scripts/release/asc_signing.py --check         # only report what is there, change nothing; fails when
                                                         # a release would (release.sh runs this first)
  python3 scripts/release/asc_signing.py --only <bundle id>  # just that target (the others' profiles stay untouched)
  python3 scripts/release/asc_signing.py --developer-id  # only the Mac app's Developer ID profiles, for the disk image
                                                         # outside the App Store (scripts/release/mac_dmg.sh); --check too

The App Group itself (group.<bundle id>, or config.json app.app_group) can't be made or put on an App ID through the
API, only on the developer website (docs/operations/release.md "App Group"): each profile is checked for it, and a missing one
prints those steps.
"""
import base64
import os
import plistlib
import subprocess
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
sys.path.insert(0, os.path.dirname(__file__))
from asc import BUNDLE_ID, CONFIG, APIError, create, get_all, request  # noqa: E402
import persona_config  # noqa: E402

APP = BUNDLE_ID
WATCH = APP + ".watchkitapp"
LIVE = APP + ".liveactivity"  # the widget extension drawing the lock screen / Dynamic Island activity
NOTIFY = APP + ".notify"  # the notification service extension opening relayed pushes (docs/operations/push-relay.md)
SHARE = APP + ".share"  # the share extension sending to Persona's main conversation (#4586)
# bundle identifier -> (App ID name, profile name, capabilities)
PROFILES = persona_config.provisioning_profiles(CONFIG)
TARGETS = {
    APP: ("Persona", PROFILES[APP], ["PUSH_NOTIFICATIONS", "USERNOTIFICATIONS_TIMESENSITIVE", "HEALTHKIT", "APP_GROUPS"]),
    WATCH: ("Persona Watch", PROFILES[WATCH], []),
    LIVE: ("Persona Live Activity", PROFILES[LIVE], ["APP_GROUPS"]),
    NOTIFY: ("Persona Notify", PROFILES[NOTIFY], []),
    SHARE: ("Persona Share", PROFILES[SHARE], []),
}
# The App Group the app shares the Live Activity's persona with the widget extension through (docs/apple/live-activity.md).
GROUP = persona_config.app_group(CONFIG)
PROFILE_DIR = os.path.expanduser("~/Library/MobileDevice/Provisioning Profiles")


def bundle_id(identifier: str, name: str, check_only: bool) -> str:
    found = [b for b in get_all("/v1/bundleIds", {"filter[identifier]": identifier})
             if b["attributes"]["identifier"] == identifier]
    if found:
        print("have bundle id", identifier, found[0]["id"])
        return found[0]["id"]
    if check_only:
        sys.exit(f"bundle id {identifier} is not registered yet; run without --check")
    print("register", identifier)
    return create("bundleIds", {"identifier": identifier, "name": name, "platform": "IOS"}, {}, "/v1/bundleIds")["id"]


def ensure_capability(bid: str, capability: str, check_only: bool):
    # This relationship doesn't page (no `limit`).
    have = {c["attributes"]["capabilityType"] for c in request("GET", f"/v1/bundleIds/{bid}/bundleIdCapabilities")["data"]}
    if capability in have:
        print("  have capability", capability)
        return
    if check_only:
        print("  missing capability", capability)
        NOT_READY.append(f"{bid}: capability {capability}")
        return
    print("  enable", capability)
    # PUSH_NOTIFICATIONS, USERNOTIFICATIONS_TIMESENSITIVE and HEALTHKIT take no settings; the API rejects any key we invent
    # ("'PUSH_NOTIFICATIONS' is not a valid value for settings/0/key").
    try:
        create("bundleIdCapabilities", {"capabilityType": capability, "settings": []},
               {"bundleId": ("bundleIds", bid)}, "/v1/bundleIdCapabilities")
    except APIError as e:
        # Entity.attribute.invalid when the capability is not offered back for this App ID.
        sys.exit(f"could not enable {capability}: {e}")


CERTIFICATE_TYPES = {
    "dist": ("DISTRIBUTION", "IOS_DISTRIBUTION"),
    "dev": ("DEVELOPMENT", "IOS_DEVELOPMENT"),
    "devid": ("DEVELOPER_ID_APPLICATION_G2", "DEVELOPER_ID_APPLICATION"),
}
# Only the Account Holder can create a Developer ID certificate: the API answers any other key with 403.
NO_DEVELOPER_ID = """No Developer ID Application certificate in the team. Only the Account Holder can create one, on the Mac that
will sign (its private key stays in that Mac's login keychain):
  Xcode › Settings › Accounts › (the team) › Manage Certificates › + › Developer ID Application,
  or developer.apple.com › Certificates › + › Developer ID Application (with a CSR from Keychain Access).
Then run this again (docs/operations/release.md "Mac disk image")."""


def certificate(kind: str) -> str:
    certs = [c for c in get_all("/v1/certificates") if c["attributes"]["certificateType"] in CERTIFICATE_TYPES[kind]]
    if not certs:
        if kind == "devid":
            sys.exit(NO_DEVELOPER_ID)
        sys.exit(f"No {'distribution' if kind == 'dist' else 'development'} certificate in the team: create one first "
                 "(see docs/operations/release.md).")
    return max(certs, key=lambda c: c["attributes"]["expirationDate"])["id"]


def groups(content: str) -> list:
    """The App Groups a profile (its base64 profileContent) allows."""
    with tempfile.NamedTemporaryFile(suffix=".mobileprovision") as f:
        f.write(base64.b64decode(content))
        f.flush()
        out = subprocess.run(["security", "cms", "-D", "-i", f.name], capture_output=True, check=True).stdout
    return plistlib.loads(out).get("Entitlements", {}).get("com.apple.security.application-groups", [])


MISSING_GROUP = []  # profiles without GROUP although their App ID should have it
# --check: what a release would trip over (a capability the entitlements need, a profile missing or made INVALID by a
# capability change). release.sh runs --check first, so this fails the release before the archive, not at the export.
NOT_READY = []


def profile(bid: str, name: str, cert: str, kind: str = "IOS_APP_STORE", check_only: bool = False,
            group: bool = False):
    # Includes INVALID ones: changing the App ID's capabilities invalidates profiles, and a stale profile with the
    # same name blocks creating the new one. Recreating is cheap and keeps it in sync with the App ID.
    existing = get_all("/v1/profiles", {"filter[name]": name})
    current = [p for p in existing if p["attributes"]["profileState"] == "ACTIVE"]
    if check_only:
        print("  profile", name, "ACTIVE" if current else "missing or invalid")
        if not current:
            NOT_READY.append(f"profile {name}: missing or invalid")
        if current and group and GROUP not in groups(current[0]["attributes"]["profileContent"]):
            print("  profile", name, "lacks the App Group", GROUP)
            MISSING_GROUP.append(name)
        return
    for p in existing:
        request("DELETE", f"/v1/profiles/{p['id']}")
    print("create profile", name)
    body = {"data": {"type": "profiles",
                     "attributes": {"name": name, "profileType": kind},
                     "relationships": {"bundleId": {"data": {"type": "bundleIds", "id": bid}},
                                       "certificates": {"data": [{"type": "certificates", "id": cert}]}}}}
    if kind in ("IOS_APP_DEVELOPMENT", "MAC_CATALYST_APP_DEVELOPMENT"):
        platform = "MAC_OS" if kind.startswith("MAC") else "IOS"
        devices = [d for d in get_all("/v1/devices")
                   if d["attributes"]["status"] == "ENABLED" and d["attributes"]["platform"] == platform]
        body["data"]["relationships"]["devices"] = {"data": [{"type": "devices", "id": d["id"]} for d in devices]}
    created = request("POST", "/v1/profiles", body)["data"]
    os.makedirs(PROFILE_DIR, exist_ok=True)
    extension = "provisionprofile" if kind.startswith("MAC") else "mobileprovision"
    path = os.path.join(PROFILE_DIR, f"{created['attributes']['uuid']}.{extension}")
    with open(path, "wb") as f:
        f.write(base64.b64decode(created["attributes"]["profileContent"]))
    print("  installed", path)
    if group and GROUP not in groups(created["attributes"]["profileContent"]):
        print("  but it lacks the App Group", GROUP)
        MISSING_GROUP.append(name)


def developer_id(check_only: bool):
    """The Mac app and its notification extension, signed with Developer ID (scripts/release/mac_dmg.sh)."""
    cert = certificate("devid")
    print("Developer ID certificate", cert)
    for identifier, profile_name in persona_config.developer_id_profiles(CONFIG).items():
        name, _, capabilities = TARGETS[identifier]
        print(identifier)
        bid = bundle_id(identifier, name, check_only)
        for capability in capabilities:
            ensure_capability(bid, capability, check_only)
        profile(bid, profile_name, cert, "MAC_APP_DIRECT", check_only)


def main():
    check_only = "--check" in sys.argv
    if "--developer-id" in sys.argv:
        developer_id(check_only)
        if NOT_READY:
            sys.exit(f"Developer ID signing is not ready: {'; '.join(NOT_READY)}. "
                     "Run python3 scripts/release/asc_signing.py --developer-id")
        return
    cert = certificate("dist")
    print("distribution certificate", cert)
    ids = {}
    only = sys.argv[sys.argv.index("--only") + 1] if "--only" in sys.argv else None
    for identifier, (name, profile_name, capabilities) in TARGETS.items():
        if only and identifier != only:
            continue
        print(identifier)
        bid = bundle_id(identifier, name, check_only)
        ids[identifier] = bid
        for capability in capabilities:
            ensure_capability(bid, capability, check_only)
        profile(bid, profile_name, cert, check_only=check_only, group="APP_GROUPS" in capabilities)
    if "--dev" in sys.argv:
        dev = certificate("dev")
        for identifier, (name, profile_name, capabilities) in TARGETS.items():
            if identifier not in ids:
                continue
            # Custom distribution names need a distinct development name too: reusing one would delete the store profile.
            development_name = (profile_name.replace("App Store", "Development") if "App Store" in profile_name
                                else profile_name + " Development")
            profile(ids[identifier], development_name, dev,
                    "IOS_APP_DEVELOPMENT", check_only, group="APP_GROUPS" in capabilities)
    if NOT_READY:
        sys.exit(f"""
Signing is not ready for a release: {"; ".join(NOT_READY)}.
Run python3 scripts/release/asc_signing.py (with --dev too if you sign development builds with these profiles).""")
    if MISSING_GROUP:
        sys.exit(f"""
{len(MISSING_GROUP)} profile(s) without the App Group {GROUP}: {", ".join(MISSING_GROUP)}.
The API can't make an App Group or put one on an App ID. On developer.apple.com › Certificates, Identifiers & Profiles:
  1. Identifiers › + › App Groups: description Persona, identifier {GROUP}.
  2. Identifiers › {APP} › App Groups › Configure (Edit): tick {GROUP}, Continue, Save, Confirm.
  3. The same for {LIVE}.
Then run this again (docs/operations/release.md「App Group」).""")
    if not check_only:
        print("\nDone. Release builds sign with these profiles (release.sh generates the export options from the config).")


if __name__ == "__main__":
    try:
        main()
    except APIError as e:
        sys.exit(str(e))
