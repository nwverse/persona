#!/usr/bin/env python3
"""Run real extension isolation in the hub runtime stage, without a hub or real state."""
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]


def run(args, timeout, **kwargs):
    print("+", " ".join(map(str, args)), flush=True)
    return subprocess.run(args, check=True, timeout=timeout, **kwargs)


def main():
    image = ""
    with tempfile.TemporaryDirectory(prefix="persona-container-sandbox-") as tmp:
        directory = Path(tmp)
        # Non-root container users can traverse the binary's bind mount.
        directory.chmod(0o755)
        binary = directory / "sandbox.test"
        try:
            run(["docker", "build", "--target", "sandbox-runtime", "--iidfile", str(directory / "image"), "."],
                120, cwd=ROOT)
            image = (directory / "image").read_text().strip()
            run(["go", "test", "-c", "-o", str(binary), "./internal/extension/sandbox"],
                180, cwd=ROOT / "server", env={**os.environ, "CGO_ENABLED": "0"})
            binary.chmod(0o755)
            result = run(["docker", "run", "--rm", "--network=none", "--cap-drop=ALL", "--user=1000:1000",
                          "--security-opt=no-new-privileges:true",
                          "--security-opt=seccomp=" + str(ROOT / "deploy/docker/extension-seccomp.json"),
                          "--security-opt=apparmor=unconfined",
                          "--mount", "type=bind,src=" + str(directory) + ",dst=/probe,readonly",
                          "--env=HOME=/home/persona", image, "/bin/sh", "-ec",
                          'test "$(id -u)" != 0; '
                          'test ! -u /usr/bin/bwrap; '
                          'grep -q "^CapEff:[[:space:]]*0000000000000000$" /proc/self/status; '
                          'exec /probe/sandbox.test -test.run=^TestLinuxSandboxForReal$ -test.v -test.timeout=45s'],
                         60, capture_output=True, text=True)
            print(result.stdout, end="", flush=True)
            if "--- PASS: TestLinuxSandboxForReal" not in result.stdout or "--- SKIP:" in result.stdout:
                raise RuntimeError("real container isolation did not run successfully")
            print("Extension container isolation: PASS (non-root, no capabilities, real filesystem/network isolation)")
        finally:
            if image:
                subprocess.run(["docker", "image", "rm", image], timeout=30, check=False)


if __name__ == "__main__":
    main()
