#!/usr/bin/env python3
"""Runs the App Store Connect CLI (`asc`, github.com/rorkai/App-Store-Connect-CLI) with this person's API key.

The key comes from the same place as the rest of the release scripts: ASC_KEY_ID, ASC_ISSUER_ID, ASC_PRIVATE_KEY_PATH
(or ASC_KEY_PATH) and ASC_APP_ID in the environment, else app_store.key_id / issuer_id / key_path / app_id in the
config (docs/operations/config.md). The .p8 file stays outside the repository; nothing here prints it or the issuer id.

  python3 scripts/release/asc_cli.py check                  # offline: binary, key, app id; no request is sent
  python3 scripts/release/asc_cli.py install                # download the pinned release and verify its sha256
  python3 scripts/release/asc_cli.py <asc arguments…>       # e.g. builds list --output table

The pinned release is downloaded once into ~/.cache/persona/asc/<version>/. ASC_BIN runs another build instead
(your own fork, `go build -o ~/bin/asc .` in its checkout). Telemetry is always off.
"""
import hashlib
import os
import platform
import stat
import sys
import tempfile
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import persona_config  # noqa: E402

VERSION = "6.4.0"
# From asc_<version>_checksums.txt of the release.
SHA256 = {
    "macOS_arm64": "2743dc84d1351ca62c1ab561dade8026f465158dc09ceff7a6125f54e7788ee1",
    "macOS_amd64": "7a278c00c3f4ec9795baefb09ee1a4d4c7c7aefc06fc6aab71d1a010950e13bf",
    "linux_arm64": "9c065a903762e3f75d9120fdc7b7c52be035f58bccc64eeba8680bb6c0d1977d",
    "linux_amd64": "756b6f7b81b7fda8856723c2ba15fc877e9a4f73f067cda957d5e881415ccf19",
}
RELEASES = "https://github.com/rorkai/App-Store-Connect-CLI/releases/download"
# Credential sources asc would otherwise mix with ours (its stored profiles, an inline key).
FOREIGN = ("ASC_PROFILE", "ASC_BYPASS_KEYCHAIN", "ASC_CONFIG_PATH", "ASC_PRIVATE_KEY", "ASC_PRIVATE_KEY_B64")
SETUP = "See docs/operations/release.md「Releasing with asc」."


def target() -> str:
    system = {"Darwin": "macOS", "Linux": "linux"}.get(platform.system(), platform.system())
    machine = {"x86_64": "amd64", "AMD64": "amd64", "aarch64": "arm64"}.get(platform.machine(), platform.machine())
    name = f"{system}_{machine}"
    if name not in SHA256:
        sys.exit(f"asc {VERSION} has no pinned build for {name}; set ASC_BIN to one you built. {SETUP}")
    return name


def cached() -> str:
    base = os.environ.get("XDG_CACHE_HOME") or os.path.expanduser("~/.cache")
    return os.path.join(base, "persona", "asc", VERSION, "asc")


def binary() -> str:
    """The asc to run: ASC_BIN if set, else the pinned release (not downloaded here)."""
    custom = os.environ.get("ASC_BIN", "")
    return os.path.expanduser(custom) if custom else cached()


def fetch(url: str) -> bytes:
    with urllib.request.urlopen(url, timeout=600) as resp:
        return resp.read()


def install() -> str:
    path = cached()
    if os.path.isfile(path):
        return path
    name = target()
    url = f"{RELEASES}/{VERSION}/asc_{VERSION}_{name}"
    print(f"downloading asc {VERSION} ({name})…", file=sys.stderr)
    data = fetch(url)
    digest = hashlib.sha256(data).hexdigest()
    if digest != SHA256[name]:
        sys.exit(f"{url}: sha256 {digest} is not the pinned {SHA256[name]}; not installed.")
    os.makedirs(os.path.dirname(path), exist_ok=True)
    fd, tmp = tempfile.mkstemp(dir=os.path.dirname(path))
    with os.fdopen(fd, "wb") as f:
        f.write(data)
    os.chmod(tmp, 0o755)
    os.replace(tmp, path)
    return path


def credentials() -> dict:
    """ASC_* for asc, from the environment first, then the config. Exits naming whatever is missing."""
    store = {}
    if os.path.exists(persona_config.path()):
        store = persona_config.load().get("app_store", {})
    out = {
        "ASC_KEY_ID": os.environ.get("ASC_KEY_ID") or store.get("key_id", ""),
        "ASC_ISSUER_ID": os.environ.get("ASC_ISSUER_ID") or store.get("issuer_id", ""),
        "ASC_PRIVATE_KEY_PATH": os.path.expanduser(
            os.environ.get("ASC_PRIVATE_KEY_PATH") or os.environ.get("ASC_KEY_PATH") or store.get("key_path", "")),
        "ASC_APP_ID": os.environ.get("ASC_APP_ID") or str(store.get("app_id", "")),
    }
    names = {"ASC_KEY_ID": "key_id", "ASC_ISSUER_ID": "issuer_id", "ASC_PRIVATE_KEY_PATH": "key_path"}
    missing = [f"{env} (app_store.{key})" for env, key in names.items() if not out[env]]
    if missing:
        sys.exit(f"no App Store Connect API key: set {', '.join(missing)} in the environment or in "
                 f"{persona_config.path()}. {SETUP}")
    key = out["ASC_PRIVATE_KEY_PATH"]
    if not os.path.isfile(key):
        sys.exit(f"the API key file {key} does not exist (ASC_PRIVATE_KEY_PATH / app_store.key_path). {SETUP}")
    if not os.access(key, os.R_OK):
        sys.exit(f"the API key file {key} is not readable. {SETUP}")
    if not out["ASC_APP_ID"]:
        del out["ASC_APP_ID"]
    return out


def child_env(creds: dict) -> dict:
    env = {k: v for k, v in os.environ.items() if k not in FOREIGN}
    env.update(creds)
    env["ASC_TELEMETRY_DISABLED"] = "1"
    env["DO_NOT_TRACK"] = "1"
    return env


def check() -> int:
    creds = credentials()
    key = creds["ASC_PRIVATE_KEY_PATH"]
    mode = stat.S_IMODE(os.stat(key).st_mode)
    path = binary()
    if os.environ.get("ASC_BIN"):
        where = f"{path} (ASC_BIN)"
        if not os.access(path, os.X_OK):
            sys.exit(f"ASC_BIN={path} is not an executable file.")
    else:
        where = f"{path} ({'installed' if os.path.isfile(path) else 'not installed yet: the first run downloads it'})"
    print(f"asc       {VERSION} pinned, {where}")
    print(f"key id    …{creds['ASC_KEY_ID'][-4:]}")
    print("issuer id set")
    print(f"key file  {key} (mode {mode:o}{', readable by others: chmod 600 it' if mode & 0o077 else ''})")
    print(f"app id    {creds.get('ASC_APP_ID', 'not set (pass --app to commands that need it)')}")
    print("ok: asc will run with this key (nothing was sent to App Store Connect)")
    return 0


def main(argv) -> int:
    if argv[:1] == ["check"]:
        return check()
    if argv[:1] == ["install"]:
        print(install())
        return 0
    offline = not argv or argv[0] in ("version", "help") or "--help" in argv or "-h" in argv
    creds = {} if offline else credentials()
    path = binary() if os.environ.get("ASC_BIN") else install()
    os.execve(path, [path, *argv], child_env(creds))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
