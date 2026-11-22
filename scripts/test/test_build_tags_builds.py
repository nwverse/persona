"""The image, deploy.sh, mac-link.sh and publish.sh build every program with server/build-tags.txt, as the release
is built (server/internal/buildtags). Nothing else fails when one of their builds drops the tags: the binary only
gets bigger."""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[2]
BUILDS = ("Dockerfile", "scripts/deploy/deploy.sh", "scripts/app/mac-link.sh", "scripts/release/publish.sh")
FROM_FILE = r'"\$\(cat (?:server/)?build-tags\.txt\)"'
TAGGED = re.compile(r'go build .*-tags (?:' + FROM_FILE + r'|"\$TAGS")')
TAGS_SET = re.compile(r"(?m)^\s*TAGS=" + FROM_FILE + r"$")


class BuildTagsBuildsTests(unittest.TestCase):
    def test_every_go_build_uses_build_tags_txt(self):
        for path in BUILDS:
            with self.subTest(path=path):
                source = (ROOT / path).read_text()
                lines = (line.strip() for line in source.splitlines())
                builds = [line for line in lines if "go build " in line and not line.startswith("#")]
                self.assertTrue(builds, "no go build found")
                for line in builds:
                    self.assertTrue(TAGGED.search(line), f"go build without server/build-tags.txt: {line}")
                if any('"$TAGS"' in line for line in builds):
                    self.assertTrue(TAGS_SET.search(source), 'TAGS is not set to "$(cat build-tags.txt)"')


if __name__ == "__main__":
    unittest.main()
