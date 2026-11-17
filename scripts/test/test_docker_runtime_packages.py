"""Every integrations/<dir> that server/internal/runtimepkg embeds (its Packages and Assets) reaches the hub image's build:
the Dockerfile copies it and .dockerignore lets it into the context. A package added to runtimepkg without both broke
`go generate ./internal/runtimepkg` in the image build only (#9626)."""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[2]
RUNTIMEPKG = ROOT / "server/internal/runtimepkg/runtimepkg.go"


def map_block(source, name):
    """The body of `var <name> = map[string][]string{ ... }`."""
    match = re.search(r"^var " + name + r" = map\[string\]\[\]string\{\n(.*?)^\}", source, re.M | re.S)
    if not match:
        raise AssertionError("runtimepkg.go has no " + name + " map")
    return match.group(1)


def embedded_dirs():
    """The top-level integrations/ directories runtimepkg's Packages and Assets patterns read."""
    source = RUNTIMEPKG.read_text()
    dirs = set()
    for name in ("Packages", "Assets"):
        body = re.sub(r"//[^\n]*", "", map_block(source, name))
        for pattern in re.findall(r'"([^"]+)"', body):
            if "/" in pattern:  # a pattern is <dir>/<path>; the map's keys (package names) have no slash
                dirs.add(pattern.split("/", 1)[0])
    return dirs


class DockerRuntimePackagesTests(unittest.TestCase):
    def test_runtimepkg_reads_known_directories(self):
        dirs = embedded_dirs()
        self.assertIn("runtime-sdk", dirs)
        self.assertIn("claude-code-plugin", dirs)
        for d in dirs:
            with self.subTest(dir=d):
                self.assertTrue((ROOT / "integrations" / d).is_dir(), d)

    def test_every_embedded_directory_is_copied_and_let_in(self):
        dockerfile = (ROOT / "Dockerfile").read_text()
        ignore = (ROOT / ".dockerignore").read_text().splitlines()
        for d in sorted(embedded_dirs()):
            with self.subTest(dir=d):
                self.assertRegex(dockerfile, r"(?m)^COPY integrations/" + re.escape(d) + r"/ /src/integrations/" + re.escape(d) + r"/$")
                self.assertIn("!integrations/" + d + "/", ignore)


if __name__ == "__main__":
    unittest.main()
