import re
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


class TranscriptStatsTests(unittest.TestCase):
    def test_overlapping_and_symlinked_roots_count_each_file_once(self):
        script = Path(__file__).with_name("transcript_stats.py")
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            parent = root / "projects"
            nested = parent / "nested"
            nested.mkdir(parents=True)
            (nested / "one.jsonl").write_text('{"type":"user"}\n')
            (parent / "other.jsonl").write_text('{"type":"assistant"}\n' * 2)
            alias = root / "alias"
            alias.symlink_to(nested, target_is_directory=True)
            result = subprocess.run([sys.executable, "-B", str(script), str(parent), str(nested), str(alias)],
                                    capture_output=True, text=True, timeout=10, check=True)
        self.assertEqual(result.stdout.splitlines()[0], "2 files, 3 lines")
        self.assertRegex(result.stdout, re.compile(r"^\s*1  claude user\s+.*:1$", re.MULTILINE))
        self.assertRegex(result.stdout, re.compile(r"^\s*2  claude assistant\s+.*:1$", re.MULTILINE))


if __name__ == "__main__":
    unittest.main()
