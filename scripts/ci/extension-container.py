#!/usr/bin/env python3
"""Run real extension isolation in the hub runtime stage, without a hub or real state."""
import json
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]


def run(args, timeout, **kwargs):
    print("+", " ".join(map(str, args)), flush=True)
    result = subprocess.run(args, timeout=timeout, **kwargs)
    if kwargs.get("capture_output"):
        print(result.stdout, end="", flush=True)
        print(result.stderr, end="", flush=True)
    result.check_returncode()
    return result


def run_probe(image, name, directory, apparmor, profile_check):
    create = ["docker", "create", "--name", name, "--network=none", "--cap-drop=ALL", "--user=1000:1000",
              "--security-opt=no-new-privileges:true",
              "--security-opt=seccomp=" + str(ROOT / "deploy/docker/extension-seccomp.json"),
              *apparmor,
              "--mount", "type=bind,src=" + str(directory) + ",dst=/probe,readonly",
              "--env=HOME=/home/persona", image, "/bin/sh", "-ec",
              'test "$(id -u)" != 0; '
              'uname -a; '
              'grep -a -F "subset=pid" /usr/bin/bwrap >/dev/null; '
              'test ! -u /usr/bin/bwrap; '
              'grep -q "^CapEff:[[:space:]]*0000000000000000$" /proc/self/status; '
              'grep -q "^NoNewPrivs:[[:space:]]*1$" /proc/self/status; '
              'grep -q "/proc/kcore" /proc/self/mountinfo; '
              'grep -q "/proc/sysrq-trigger" /proc/self/mountinfo; '
              + profile_check +
              '/probe/sandbox.test -test.run=^TestLinuxSandboxForReal$ -test.v -test.timeout=45s; '
              '/probe/extmgr.test -test.run="^TestExtensionSandbox(AgentRuntime|Missing|MissingOfficialExemption)?$" -test.v -test.timeout=60s']
    run(create, 30)
    details = json.loads(run(["docker", "inspect", name], 15, capture_output=True, text=True).stdout)[0]
    host = details["HostConfig"]
    if apparmor:
        expected_profile = apparmor[0].rsplit("=", 1)[1]
        actual_profile = details.get("AppArmorProfile")
        print("AppArmor inspect: expected=%r actual=%r" % (expected_profile, actual_profile), flush=True)
        if actual_profile != expected_profile:
            raise RuntimeError("container did not use the required AppArmor profile")
    for option in host.get("SecurityOpt") or []:
        if "unconfined" in option:
            raise RuntimeError("an unconfined override is not a supported isolation result")
    # Nil inherits the OCI defaults. An explicitly empty or incomplete non-nil list is not equivalent.
    masked = host.get("MaskedPaths")
    if masked is not None and "/proc/kcore" not in set(masked):
        raise RuntimeError("Docker protected proc masks are missing")
    readonly = host.get("ReadonlyPaths")
    if readonly is not None:
        for path in ("/proc/sys", "/proc/sysrq-trigger"):
            if path not in set(readonly):
                raise RuntimeError("Docker protected readonly proc paths are missing: " + path)
    return subprocess.run(["docker", "start", "--attach", name], capture_output=True, text=True, timeout=120)


def main():
    image = ""
    stock_image = ""
    with tempfile.TemporaryDirectory(prefix="persona-container-sandbox-") as tmp:
        directory = Path(tmp)
        # Non-root container users can traverse the binary's bind mount.
        directory.chmod(0o755)
        container = directory.name
        try:
            run(["docker", "build", "--target", "sandbox-runtime-stock", "--iidfile", str(directory / "image-stock"), "."],
                180, cwd=ROOT)
            stock_image = (directory / "image-stock").read_text().strip()
            run(["docker", "build", "--target", "sandbox-runtime", "--iidfile", str(directory / "image"), "."],
                180, cwd=ROOT)
            image = (directory / "image").read_text().strip()
            # Parse the optional host policy in a disposable userspace; never load host LSM policy.
            policy = directory / "persona-hub.apparmor"
            policy.write_text((ROOT / "deploy/docker/persona-hub.apparmor").read_text())
            run(["docker", "run", "--rm", "--name", container + "-policy",
                 "--mount", "type=bind,src=" + str(directory) + ",dst=/probe,readonly", "ubuntu:24.04",
                 "/bin/sh", "-ec", "apt-get update -qq && DEBIAN_FRONTEND=noninteractive "
                 "apt-get install -y -qq --no-install-recommends apparmor && "
                 "apparmor_parser -Q -K /probe/persona-hub.apparmor"], 180)
            security = run(["docker", "info", "--format", "{{json .SecurityOptions}}"], 15,
                           capture_output=True, text=True)
            apparmor = ["--security-opt=apparmor=persona-hub"] if "apparmor" in security.stdout.lower() else []
            if os.environ.get("PERSONA_REQUIRE_APPARMOR") == "1" and not apparmor:
                raise RuntimeError("this hosted isolation run requires AppArmor, but Docker does not advertise it")
            print("AppArmor: named host profile required" if apparmor else "AppArmor: disabled on this test host; profile syntax checked", flush=True)
            checks = {
                "sandbox": ["TestLinuxSandboxForReal"],
                "extmgr": ["TestExtensionSandbox", "TestExtensionSandboxAgentRuntime", "TestExtensionSandboxMissing", "TestExtensionSandboxMissingOfficialExemption"],
            }
            for package in checks:
                binary = directory / (package + ".test")
                source = "./internal/extension/sandbox" if package == "sandbox" else "./internal/extmgr"
                run(["go", "test", "-c", "-o", str(binary), source],
                    180, cwd=ROOT / "server", env={**os.environ, "CGO_ENABLED": "0"})
                binary.chmod(0o755)
            profile_check = ""
            if apparmor:
                profile_name = apparmor[0].rsplit("=", 1)[1]
                profile_check = 'grep -qx "' + profile_name + ' (enforce)" /proc/self/attr/current; '
            stock = subprocess.run(["docker", "run", "--rm", "--name", container + "-stock", "--network=none", "--cap-drop=ALL",
                                    "--user=1000:1000", "--security-opt=no-new-privileges:true",
                                    "--security-opt=seccomp=" + str(ROOT / "deploy/docker/extension-seccomp.json"), *apparmor,
                                    stock_image, "/usr/bin/bwrap", "--die-with-parent", "--unshare-user-try", "--unshare-pid",
                                    "--unshare-net", "--proc", "/proc", "--dev", "/dev", "--", "/bin/true"],
                                   capture_output=True, text=True, timeout=60)
            print(stock.stdout, end="", flush=True)
            print(stock.stderr, end="", flush=True)
            if stock.returncode == 0 or "Can't mount proc on /proc" not in stock.stdout + stock.stderr:
                raise RuntimeError("stock bubblewrap did not fail closed on protected proc")
            print("Stock protected-proc negative control: PASS (expected failure)", flush=True)
            subprocess.run(["docker", "rm", "-f", container + "-stock"], timeout=30, check=False, capture_output=True)
            result = run_probe(image, container, directory, apparmor, profile_check)
            print(result.stdout, end="", flush=True)
            print(result.stderr, end="", flush=True)
            result.check_returncode()
            for tests in checks.values():
                for test in tests:
                    if "--- PASS: " + test + " (" not in result.stdout:
                        raise RuntimeError("real container isolation did not pass " + test)
            if "--- SKIP:" in result.stdout:
                raise RuntimeError("real container isolation skipped a required check")
            canary = run(["docker", "run", "--rm", "--name", container + "-subset", "--network=none", "--cap-drop=ALL",
                          "--user=1000:1000", "--security-opt=no-new-privileges:true",
                          "--security-opt=seccomp=" + str(ROOT / "deploy/docker/extension-seccomp.json"), *apparmor,
                          image, "/bin/sh", "-ec",
                          "outer_net=$(readlink /proc/self/ns/net); outer_pid=$(readlink /proc/self/ns/pid); "
                          "printf 'OUTER_NET=%s\\nOUTER_PID=%s\\n' \"$outer_net\" \"$outer_pid\"; "
                          "/usr/bin/bwrap --die-with-parent --unshare-user-try --unshare-pid --unshare-net "
                          "--proc /proc --dev /dev -- /bin/sh -ec "
                          "'printf \"INNER_NET=%s\\nINNER_PID=%s\\n\" \"$(readlink /proc/self/ns/net)\" "
                          "\"$(readlink /proc/self/ns/pid)\"; "
                          "grep -q '^NoNewPrivs:[[:space:]]*1$' /proc/self/status && test -r /proc/self/status && "
                          "grep -q subset=pid /proc/self/mountinfo && test ! -e /proc/sys && "
                          "test ! -e /proc/sysrq-trigger && test ! -e /proc/kcore'"],
                         60, capture_output=True, text=True)

            def namespace(key):
                prefix = key + "="
                for line in canary.stdout.splitlines():
                    if line.startswith(prefix):
                        return line[len(prefix):]
                raise RuntimeError("missing namespace evidence: " + key)

            if namespace("OUTER_NET") == namespace("INNER_NET") or namespace("OUTER_PID") == namespace("INNER_PID"):
                raise RuntimeError("bubblewrap did not create fresh PID and network namespaces")
            print("Restricted proc and fresh PID/network namespaces: PASS", flush=True)
            print("Extension container isolation: PASS (non-root, no capabilities, real filesystem/network isolation)")
        finally:
            subprocess.run(["docker", "rm", "-f", container, container + "-stock", container + "-subset", container + "-policy"],
                           timeout=30, check=False, capture_output=True)
            if stock_image:
                subprocess.run(["docker", "image", "rm", stock_image], timeout=30, check=False)
            if image:
                subprocess.run(["docker", "image", "rm", image], timeout=30, check=False)


if __name__ == "__main__":
    main()
