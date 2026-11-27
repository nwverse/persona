#!/usr/bin/env python3
"""Exercise the real iOS app against real core-only and full hubs (#17267).

    python3 scripts/ci/ios-core-only-hub.py --shots ~/persona-pr-shots/ios-core-only

Builds the default iOS app once from a disposable source snapshot, then starts each server edition in its own temporary
HOME/PERSONA_HOME with a disposable Postgres 18 container. The app pairs through a real single-use pair code, not the
hub's master token. Runtime CLI assertions cover pairing, Agents, Inbox, To-dos and module surfaces; light/dark captures
and sanitized JSON evidence go below --shots. Needs macOS, Xcode, Go and Docker. This is an opt-in local gate, not a
hosted-CI step. Prints one PASS / FAIL / TIMEOUT verdict.
"""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import secrets
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[2]


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


smoke = load_module("core_only_smoke", ROOT / "scripts/ci/core-only-smoke.py")
Failed, TimedOut, check = smoke.Failed, smoke.TimedOut, smoke.check
sys.path.insert(0, str(ROOT / "scripts/app"))
from personactl import Ctl, CtlError  # noqa: E402


def run(args, timeout=120, cwd=ROOT, env=None):
    try:
        return subprocess.run([str(a) for a in args], cwd=cwd, env=env, capture_output=True, text=True, timeout=timeout)
    except subprocess.TimeoutExpired as error:
        raise TimedOut(f"{Path(str(args[0])).name} exceeded {timeout}s") from error


def run_log(args, log, timeout=900, cwd=ROOT, env=None):
    with open(log, "w") as output:
        try:
            result = subprocess.run([str(a) for a in args], cwd=cwd, env=env, stdout=output,
                                    stderr=subprocess.STDOUT, timeout=timeout)
        except subprocess.TimeoutExpired as error:
            raise TimedOut(f"{Path(str(args[0])).name} exceeded {timeout}s; log {log}") from error
    if result.returncode:
        raise Failed(f"{Path(str(args[0])).name} exited {result.returncode}; log {log}")


def idle_reading():
    """Record the owner's required pre-build evidence; scheduling/policy stays with the caller."""
    now = run(["date", "+%Y-%m-%dT%H:%M:%S%z"], 10).stdout.strip()
    result = run(["ioreg", "-c", "IOHIDSystem"], 20)
    seconds = None
    for line in result.stdout.splitlines():
        if "HIDIdleTime" in line:
            seconds = int(line.rsplit("=", 1)[1].strip()) // 1_000_000_000
    reading = {"local_time": now, "idle_seconds": seconds}
    print(f"build-reading: local_time={now} idle_seconds={seconds}", flush=True)
    return reading


def copy_source(source):
    """Copy tracked and locally edited inputs, never ignored credentials, DerivedData or existing screenshots."""
    listed = run(["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"], 120).stdout
    for raw in listed.split("\0"):
        if not raw:
            continue
        relative = raw
        src, dst = ROOT / relative, source / relative
        dst.parent.mkdir(parents=True, exist_ok=True)
        if src.is_symlink():
            os.symlink(os.readlink(src), dst)
        elif src.is_file():
            shutil.copy2(src, dst)


def start_hub(bindir, home, work, log):
    env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "HOME": str(home), "TMPDIR": str(work),
           "PERSONA_HOME": str(home / ".persona"), "LANG": "C.UTF-8", "PERSONA_LOCAL_NODE": "0",
           "PERSONA_HUB_EDGE": "0"}
    with open(log, "a") as out:
        return subprocess.Popen(["env", "-i"] + [f"{key}={value}" for key, value in env.items()] +
                                [str(bindir / "persona-hub"), "run"], cwd=work, env={"PATH": env["PATH"]},
                                stdout=out, stderr=subprocess.STDOUT, start_new_session=True)


def request(base, path, token, method="GET", body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(base + path, data=data, method=method,
                                headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=10) as response:
            return response.status, json.loads(response.read())
    except urllib.error.HTTPError as error:
        raw = error.read()
        try:
            payload = json.loads(raw)
        except ValueError:
            payload = {"error": error.reason}
        return error.code, payload


def pair_code(base, master):
    status, answer = request(base, "/api/v1/auth/pair-codes", master, "POST", {})
    check(status == 200, f"creating a single-use pair code returned {status}")
    token = answer.get("token", "")
    check(token.startswith("jp_") and answer.get("hub_id"), "the pair-code response is not a current single-use code")
    return token


def core_reads(base, token):
    paths = {"sessions": "/api/v1/sessions", "inbox_items": "/api/v1/inbox", "todos": "/api/v1/todos"}
    out = {}
    for name, path in paths.items():
        status, answer = request(base, path, token)
        check(status == 200, f"core read {path} returned {status}")
        key = {"sessions": "sessions", "inbox_items": "items", "todos": "todos"}[name]
        values = answer.get(key)
        check(isinstance(values, list) and not values, f"fresh-hub {path} did not return a well-formed empty list")
        out[name] = len(values)
    return out


def prepare_app(source, work, simulator, readings):
    config = work / "public-config.json"
    config.write_text(json.dumps({"app": {"bundle_id": "org.example.persona.ci", "team_id": ""}}))
    env = dict(os.environ, PERSONA_CONFIG=str(config), PERSONA_META_WEARABLES="NO",
               PERSONA_LEAN_DEV_BUILD="YES", PERSONA_HOME=str(work / "client-home/.persona"))
    run_log(["python3", "scripts/persona_config.py", "xcconfig"], work / "app-config.log", cwd=source, env=env)
    readings.append({"build": "rust-simulator", **idle_reading()})
    run_log(["native/app-core/scripts/build-apple.sh", "--simulator"], work / "app-core.log", 1200, source, env)
    run_log(["xcodegen", "generate", "-q"], work / "xcodegen.log", 120, source / "apple", env)
    derived = work / "DerivedData"
    resolve = ["xcodebuild", "-project", "apple/Persona.xcodeproj", "-scheme", "Persona", "-configuration", "Debug",
               "-destination", f"platform=iOS Simulator,id={simulator}", "-derivedDataPath", derived,
               "-clonedSourcePackagesDirPath", work / "SourcePackages", "-resolvePackageDependencies"]
    # Xcode 26.0 intermittently crashes while inserting freshly resolved packages into its IDE group. Retry only that
    # observed IDE crash once; source/compiler failures remain fatal.
    for attempt in range(2):
        readings.append({"build": f"swift-packages-{attempt + 1}", **idle_reading()})
        try:
            run_log(resolve, work / f"swift-packages-{attempt + 1}.log", 900, source, env)
            break
        except Failed:
            log = work / f"swift-packages-{attempt + 1}.log"
            if attempt or "NSMutableArray insertObjects" not in log.read_text():
                raise
            print("Xcode 26.0 package-group crash; one bounded resolution retry", flush=True)
    command = ["python3", "scripts/heavy_slot.py", "run", "--kind", "xcode", "--", "xcodebuild",
               "-project", "apple/Persona.xcodeproj", "-scheme", "Persona", "-configuration", "Debug",
               "-destination", f"platform=iOS Simulator,id={simulator}", "-derivedDataPath", derived,
               "-clonedSourcePackagesDirPath", work / "SourcePackages", "CODE_SIGNING_ALLOWED=YES",
               "CODE_SIGN_IDENTITY=-", "CODE_SIGN_STYLE=Manual", "DEVELOPMENT_TEAM=",
               "PROVISIONING_PROFILE_SPECIFIER=", "build"]
    readings.append({"build": "ios-app", **idle_reading()})
    run_log(command, work / "ios-build.log", 2400, source, env)
    app = derived / "Build/Products/Debug-iphonesimulator/Persona.app"
    check((app / "Info.plist").is_file(), f"the iOS build produced no app at {app}")
    return app, env


def wait_for(ctl, what, seconds, probe):
    deadline = time.monotonic() + seconds
    last = None
    while time.monotonic() < deadline:
        try:
            last = probe()
            if last:
                return last
        except (OSError, CtlError, ValueError) as error:
            last = str(error)
        time.sleep(0.2)
    raise Failed(f"timed out waiting for {what}: {last}")


def elements(ctl):
    return ctl.call("tree", containers=True).get("elements") or []


def matches(rows, element_id=None, label=None, label_contains=None):
    return [row for row in rows if (element_id is None or row.get("id") == element_id) and
            (label is None or row.get("label") == label) and
            (label_contains is None or label_contains in (row.get("label") or ""))]


def require(ctl, element_id=None, label=None, label_contains=None, seconds=30):
    return wait_for(ctl, element_id or label or label_contains, seconds,
                    lambda: matches(elements(ctl), element_id, label, label_contains) or None)


def has(ctl, element_id):
    return bool(matches(elements(ctl), element_id=element_id))


def has_label(ctl, label):
    return bool(matches(elements(ctl), label=label))


def ensure_active(ctl):
    """Dismiss a SpringBoard permission/notice alert on this run's synthetic simulator, never on a physical device."""
    state = ctl.call("state")
    if state.get("appState") == "active":
        return True
    now = time.monotonic()
    if now - getattr(ctl, "last_alert_attempt", 0) < 1:
        return False
    ctl.last_alert_attempt = now
    for label in ("不允许", "Don't Allow", "Don’t Allow", "Not Now", "允许", "Allow", "OK", "好"):
        result = run(["idb", "ui", "tap", label, "--udid", ctl.udid], 30)
        if result.returncode == 0:
            print(f"dismissed a system interruption on the owned simulator ({label})", flush=True)
            wait_for(ctl, "active app after a system interruption", 10,
                     lambda: ctl.call("state").get("appState") == "active" or None)
            return True
    return False


def no_errors(ctl, page):
    ensure_active(ctl)
    rows = elements(ctl)
    bad_ids = {"inbox.offline", "todos.offline"}
    bad = [row.get("id") for row in rows if row.get("id") in bad_ids]
    phrases = ("couldn't load", "failed to load", "pairing expired", "invalid token", "unable to load")
    bad += [row.get("label") for row in rows if any(p in (row.get("label") or "").lower() for p in phrases)]
    check(not bad, f"{page} showed an error state: {bad}")
    check(ctl.call("state").get("appState") == "active", f"{page}: the app is not active")


def capture(ctl, shots, edition, page, appearance, evidence):
    ctl.appearance(appearance)
    path = shots / f"{edition}-{appearance}-{page}.png"
    ctl.screenshot(str(path), timeout=6, stable=2, pixels=600)
    check(path.stat().st_size > 1000, f"empty capture {path}")
    evidence.setdefault("screenshots", []).append(path.name)


def capture_both(ctl, shots, edition, page, evidence):
    capture(ctl, shots, edition, page, "light", evidence)
    capture(ctl, shots, edition, page, "dark", evidence)


def navigate(ctl, scene, defaults=None, session=None):
    args = {"scene": scene, "coldChats": False}
    if defaults is not None:
        args["defaults"] = defaults
    if session is not None:
        args["session"] = session
    return ctl.call("navigate", **args)


def launch(ctl, extra):
    """Launch this simulator's app with a fresh runtime token, retaining its issued hub credential when extra has no pair link."""
    ctl.token = secrets.token_hex(16)
    subprocess.run(["xcrun", "simctl", "terminate", ctl.udid, ctl.bundle], capture_output=True)
    result = run(["xcrun", "simctl", "launch", ctl.udid, ctl.bundle,
                  "-runtimeCLI", str(ctl.port), "-runtimeCLIToken", ctl.token] + extra, 30)
    check(result.returncode == 0, f"launching the app returned {result.returncode}")
    wait_for(ctl, "the DEBUG runtime channel", 30, lambda: ctl.call("ping", _timeout=2))


def settings(ctl, key):
    # SettingsView opens a DEBUG destination once per process. Relaunch for each destination and prove the path opened,
    # rather than mistaking an unchanged Settings home for a hidden module entry.
    extra = ["-tab", "settings", "-AppleLanguages", "(en)", "-AppleLocale", "en_US", "-showScreenVideoDemo", "YES"]
    if key:
        extra += ["-" + key, "YES"]
    launch(ctl, extra)
    ctl.online(timeout=30)
    if key:
        wait_for(ctl, f"Settings destination {key}", 30,
                 lambda: (ctl.call("state").get("settingsDepth") or 0) > 0 or None)


def exercise_app(ctl, base, code, edition, shots):
    full = edition == "full"
    proof = {"schema": 1, "run_id": ctl.run_id, "edition": edition, "core_reads": core_reads(base, code)}
    ctl.hub = base
    pair = "persona://pair?url=" + urllib.parse.quote(base, safe="") + "&token=" + urllib.parse.quote(code, safe="")
    # No -pairURL: its presence deliberately suppresses the device-credential exchange.
    launch(ctl, ["-openWelcome", "YES", "-openPairLink", pair,
                 "-AppleLanguages", "(en)", "-AppleLocale", "en_US",
                 "-showScreenVideoDemo", "YES"])

    def paired():
        state = ctl.call("state")
        if state.get("paired") is True and state.get("appState") != "active":
            ensure_active(ctl)
            state = ctl.call("state")
        return state if state.get("paired") is True and state.get("appState") == "active" else None

    state = wait_for(ctl, "real pairing and an active app", 60, paired)
    ctl.online(timeout=45)
    state = ctl.call("state")
    check(state.get("paired") is True, "the app did not stay paired")
    main_session = state.get("assistant")
    off = set(state.get("modulesOffByHub") or [])
    active = set(state.get("modules") or [])
    expected_registry = {"health", "screen-share", "voice"}
    check(off == set() if full else off == expected_registry,
          f"{edition}: wrong hub-off registry: {sorted(off)}")
    check(expected_registry <= active if full else not (active & expected_registry),
          f"{edition}: wrong active module registry: {sorted(active)}")
    proof["modules_marker"] = True
    proof["registry"] = {"active": sorted(active), "off_by_hub": sorted(off)}
    proof["pages"] = {}
    proof["entries"] = {}

    # Post-pairing setup is part of the real first-run path, not a mocked page.
    for control in ("engine.save", "checklist.later", "checklist.done"):
        if has(ctl, control):
            ctl.tap(id=control)
            ctl.call("settle", timeout=4, stable=2, pixels=600)
    capture_both(ctl, shots, edition, "assistant", proof)

    navigate(ctl, "agents")
    require(ctl, label="No computers yet")
    require(ctl, element_id="agents-new")
    no_errors(ctl, "Agents")
    proof["pages"]["agents"] = "loaded empty"
    capture_both(ctl, shots, edition, "agents", proof)

    navigate(ctl, "inbox")
    require(ctl, label="All Caught Up")
    no_errors(ctl, "Inbox")
    proof["pages"]["inbox"] = "loaded empty"
    capture_both(ctl, shots, edition, "inbox", proof)

    navigate(ctl, "assistant", {"openShellPage": "todos"})
    require(ctl, label="No to-dos yet")
    no_errors(ctl, "To-dos")
    proof["pages"]["todos"] = "loaded empty"
    capture_both(ctl, shots, edition, "todos", proof)

    navigate(ctl, "home", {"openShellPage": False})
    require(ctl, element_id="assistant-sidebar-button")
    ctl.tap(id="assistant-sidebar-button")
    require(ctl, element_id="sidebar-inbox")
    for entry in ("sidebar-health", "sidebar-meeting-recording"):
        proof["entries"][entry] = has(ctl, entry)
    check(proof["entries"]["sidebar-health"] == full, f"{edition}: wrong Health drawer visibility")
    check(proof["entries"]["sidebar-meeting-recording"], f"{edition}: the core recording archive entry is missing")
    no_errors(ctl, "drawer")
    capture_both(ctl, shots, edition, "drawer", proof)

    settings(ctl, None)
    require(ctl, element_id="settings-list")
    require(ctl, element_id="settings.connectors")
    no_errors(ctl, "Settings")
    proof["pages"]["settings"] = "loaded"
    capture_both(ctl, shots, edition, "settings", proof)

    settings(ctl, "openAdvancedSettings")
    require(ctl, element_id="settings-live-screen") if full else None
    for entry in ("settings-live-screen", "settings-meetings", "settings-health"):
        proof["entries"][entry] = has(ctl, entry)
    capture_both(ctl, shots, edition, "advanced-settings", proof)
    for entry in ("settings-live-screen", "settings-meetings", "settings-health"):
        check(proof["entries"][entry] == full, f"{edition}: wrong {entry} visibility")
    size = run(["xcrun", "simctl", "ui", ctl.udid, "content_size", "accessibility-extra-extra-extra-large"], 30)
    check(size.returncode == 0, "could not set the largest accessibility text size")
    capture_both(ctl, shots, edition, "advanced-settings-ax5", proof)
    size = run(["xcrun", "simctl", "ui", ctl.udid, "content_size", "large"], 30)
    check(size.returncode == 0, "could not restore the default text size")
    capture_both(ctl, shots, edition, "advanced-settings", proof)

    settings(ctl, "openConnectors")
    require(ctl, element_id="connectors-list")
    # Connector rows use visible titles, not registry accessibility identifiers.
    mail = has_label(ctl, "Mail")
    lark = has_label(ctl, "Lark")
    bot = has_label(ctl, "Lark Bot")
    proof["entries"]["settings-mail"] = mail
    proof["entries"]["connector-lark"] = lark
    proof["entries"]["connector-lark-bot"] = bot
    capture_both(ctl, shots, edition, "connectors", proof)
    check(mail == full, f"{edition}: wrong Mail connector visibility")
    check(lark == full, f"{edition}: wrong Lark connector visibility")
    check(bot == full, f"{edition}: wrong Lark Bot connector visibility")
    no_errors(ctl, "Connectors")

    settings(ctl, "openChannels")
    require(ctl, label="Channels")
    channel = has(ctl, "channel-feishu")
    proof["entries"]["channel-feishu"] = channel
    check(channel == full, f"{edition}: wrong Lark channel visibility")
    no_errors(ctl, "Channels")
    capture_both(ctl, shots, edition, "channels", proof)

    settings(ctl, "openExtensions")
    require(ctl, label="Extensions")
    voice = has(ctl, "extension-voice") or bool(matches(elements(ctl), label="Voice"))
    proof["entries"]["voice-extension"] = voice
    check(voice == full, f"{edition}: wrong Voice extension visibility")
    no_errors(ctl, "Extensions")
    capture_both(ctl, shots, edition, "extensions", proof)

    settings(ctl, "openDevices")
    require(ctl, label_contains="This Device")
    proof["current_device_confirmed"] = True
    no_errors(ctl, "Devices")
    capture_both(ctl, shots, edition, "devices", proof)

    check(isinstance(main_session, str) and main_session, f"{edition}: no real main conversation was selected after pairing")
    navigate(ctl, "assistant", {"openShellPage": False}, main_session)
    require(ctl, element_id="shell-composer-add")
    ctl.tap(id="shell-composer-add")
    require(ctl, element_id="composer-menu-mention")
    meeting = has(ctl, "composer-menu-meeting")
    proof["entries"]["composer-menu-meeting"] = meeting
    check(meeting == full, f"{edition}: wrong recording composer visibility")
    no_errors(ctl, "composer")
    capture_both(ctl, shots, edition, "composer", proof)

    ctl.call("ping", _timeout=5)
    status, devices = request(base, "/api/v1/devices", code)
    check(status == 200 and isinstance(devices.get("devices"), list) and devices["devices"],
          f"{edition}: the hub lists no paired device")
    proof["paired_devices"] = len(devices["devices"])
    # A second exchange is the protocol proof that the app redeemed the single-use code for a device credential.
    status, _ = request(base, "/api/v1/auth/tokens", code, "POST", {})
    check(status == 401, f"{edition}: the single-use pair code exchanged again with status {status}")
    proof["device_credential_issued"] = True
    proof["app_alive_at_end"] = True
    return proof


def validate_proof(proof, edition, run_id, shots):
    full = edition == "full"
    check(proof.get("schema") == 1 and proof.get("run_id") == run_id and proof.get("edition") == edition,
          "the iOS proof does not belong to this hub phase")
    check(proof.get("device_credential_issued") is True and proof.get("current_device_confirmed") is True,
          f"{edition}: no issued/current device proof")
    check(proof.get("core_reads") == {"sessions": 0, "inbox_items": 0, "todos": 0},
          f"{edition}: core API reads are missing or not empty")
    check(proof.get("paired_devices", 0) >= 1, f"{edition}: the hub lists no paired device")
    expected_registry = {"health", "screen-share", "voice"}
    registry = proof.get("registry") or {}
    check(set(registry.get("off_by_hub") or []) == (set() if full else expected_registry) and
          (expected_registry <= set(registry.get("active") or [])) == full,
          f"{edition}: the real hello did not drive the app registry")
    flags = set(proof.get("module_flags") or [])
    check("modules" in flags, f"{edition}: the proof lacks the real modules marker")
    for module in smoke.module_ids():
        check((f"module.{module}" in flags) == full, f"{edition}: module.{module} has the wrong live flag")
    check(proof.get("pages", {}).get("agents") == "loaded empty" and
          proof["pages"].get("inbox") == "loaded empty" and proof["pages"].get("todos") == "loaded empty",
          f"{edition}: a core page was not exercised")
    check(proof.get("app_alive_at_end") is True, f"{edition}: the app did not answer at the end")
    captures = proof.get("screenshots") or []
    check(len(captures) >= 20 and all((shots / name).is_file() for name in captures),
          f"{edition}: expected light/dark captures are missing")
    for name in ("sidebar-health", "settings-live-screen", "settings-meetings", "settings-health",
                 "settings-mail", "connector-lark", "connector-lark-bot", "channel-feishu", "voice-extension",
                 "composer-menu-meeting"):
        check(proof.get("entries", {}).get(name) is full, f"{edition}: {name} has the wrong proof value")


def run_phase(parent, source, app, client_env, simulator, shots, edition, run_id, keep, readings):
    work = parent / edition
    work.mkdir()
    bindir, home, log = work / "bin", work / "home", work / "hub.log"
    container = f"persona-ios-{run_id}-{edition}"
    hub = None
    try:
        readings.append({"build": f"server-{edition}", **idle_reading()})
        smoke.build("persona_core_only" if edition == "core-only" else "", bindir)
        database = smoke.postgres(container)
        port, master = smoke.free_port(), secrets.token_hex(24)
        smoke.write_hub_json(home, port, master, database)
        base = f"http://127.0.0.1:{port}"
        print(f"{edition}: PERSONA_HOME={home / '.persona'}; sandbox hub will listen on {base}", flush=True)
        hub = start_hub(bindir, home, work, log)
        loaded = smoke.wait_ready(hub, base, log, 0)
        ids = smoke.module_ids()
        check(set(loaded) == set(ids) if edition == "full" else not loaded,
              f"{edition}: wrong manager modules: {loaded}")
        status, health = request(base, "/api/v1/health", master)
        check(status == 200 and health.get("ok") is True and health.get("hub_id"), f"{edition}: unhealthy sandbox")
        features = set(health.get("features") or [])
        check("modules" in features, f"{edition}: no modules marker")
        for module in ids:
            check((f"module.{module}" in features) == (edition == "full"),
                  f"{edition}: wrong live flag for module.{module}")
        print(f"{edition}: confirmed loopback hub and real module flags; iOS pairing may proceed", flush=True)

        subprocess.run(["xcrun", "simctl", "terminate", simulator, "org.example.persona.ci"], capture_output=True)
        subprocess.run(["xcrun", "simctl", "uninstall", simulator, "org.example.persona.ci"], capture_output=True)
        run_log(["xcrun", "simctl", "install", simulator, app], work / "install.log", 120, env=client_env)
        ctl = Ctl(udid=simulator, bundle="org.example.persona.ci", port=smoke.free_port())
        ctl.run_id = run_id
        proof = exercise_app(ctl, base, pair_code(base, master), edition, shots)
        proof["hub_id"] = health["hub_id"]
        proof["module_flags"] = sorted(f for f in features if f == "modules" or f.startswith("module."))
        validate_proof(proof, edition, run_id, shots)
        (shots / f"{edition}-proof.json").write_text(json.dumps(proof, indent=2, sort_keys=True) + "\n")
        smoke.stop_hub(hub, bindir)
        hub = None
        print(f"PASS {edition}: paired with a redeemed device credential; core pages loaded; module surfaces compared", flush=True)
        return proof
    except Exception:
        if log.exists():
            sys.stderr.write(log.read_text()[-4000:])
        raise
    finally:
        if hub is not None and hub.poll() is None:
            os.killpg(hub.pid, signal.SIGKILL)
            hub.wait(10)
        subprocess.run([smoke.DOCKER, "rm", "-fv", container], capture_output=True, timeout=60)


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--shots", type=Path, required=True, help="empty output directory outside the repository")
    parser.add_argument("--keep", action="store_true", help="keep temporary hub homes/build snapshot (synthetic credentials)")
    args = parser.parse_args()
    captures_dir = args.shots.expanduser().resolve()
    check(ROOT != captures_dir and ROOT not in captures_dir.parents, "screenshots must stay outside the repository")
    check(not captures_dir.exists() or not any(captures_dir.iterdir()), "--shots must be empty; stale captures are not evidence")
    captures_dir.mkdir(parents=True, exist_ok=True)
    work = Path(tempfile.mkdtemp(prefix="persona-ios-hub-editions."))
    run_id = secrets.token_hex(6)
    simulator = None
    readings = []
    started = time.monotonic()
    verdict, detail, code = "FAIL", "not run", 1
    try:
        check(sys.platform == "darwin" and shutil.which("go") and shutil.which("xcodebuild"),
              "the iOS live comparison needs macOS, Go and Xcode")
        guard = Path.home() / ".local/bin/sim-audio-guard"
        if guard.is_file():
            run_log([guard, "--ensure"], captures_dir / "audio-guard.log", 60)
        boot = run(["scripts/app/sim-boot.sh", f"Persona Core {run_id}"], 1200,
                   env=dict(os.environ, SIM_REUSE="0"))
        check(boot.returncode == 0, f"sim-boot failed: {boot.stderr[-1000:]}")
        simulator = boot.stdout.strip().splitlines()[-1]
        print(f"owned simulator: {simulator}", flush=True)
        source = work / "source"
        copy_source(source)
        app, client_env = prepare_app(source, work, simulator, readings)
        evidence = {}
        for edition in ("core-only", "full"):
            evidence[edition] = run_phase(work, source, app, client_env, simulator, captures_dir,
                                          edition, run_id, args.keep, readings)
        (captures_dir / "evidence.json").write_text(json.dumps({
            "schema": 1, "run_id": run_id, "commit": run(["git", "rev-parse", "HEAD"], 10).stdout.strip(),
            "xcode": run(["xcodebuild", "-version"], 20).stdout.strip(), "phases": evidence,
            "build_readings": readings,
        }, indent=2, sort_keys=True) + "\n")
        verdict, detail, code = "PASS", (f"iOS paired and rendered both real hub editions; core pages and module surfaces "
                                                f"compared ({time.monotonic() - started:.0f}s)"), 0
    except (Failed, TimedOut) as error:
        verdict, detail = "TIMEOUT" if isinstance(error, TimedOut) else "FAIL", str(error)
    except Exception as error:
        verdict, detail = "FAIL", f"{type(error).__name__}: {error}"
    finally:
        if simulator:
            subprocess.run(["xcrun", "simctl", "shutdown", simulator], capture_output=True, timeout=60)
            subprocess.run(["xcrun", "simctl", "delete", simulator], capture_output=True, timeout=60)
        if not args.keep:
            shutil.rmtree(work, ignore_errors=True)
        else:
            print(f"kept {work}")
    print(f"{verdict} iOS hub editions: {detail}")
    return code


if __name__ == "__main__":
    sys.exit(main())
