"""Unit tests for the Apple measurement-only spec and package reporter (#6871)."""
import contextlib
import hashlib
import io
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

ROOT = pathlib.Path(__file__).resolve().parents[3]
SCRIPTS = ROOT / "native" / "app-core" / "scripts"


class AppleMeasurementTests(unittest.TestCase):
    def test_no_core_removes_only_two_target_dependencies(self):
        with tempfile.TemporaryDirectory() as directory, tempfile.TemporaryDirectory() as external:
            root = pathlib.Path(directory)
            (root / "apple").mkdir()
            original = (ROOT / "apple" / "project.yml").read_bytes()
            (root / "apple" / "project.yml").write_bytes(original)
            spec = pathlib.Path(external) / "generated" / "project.yml"
            result = subprocess.run([sys.executable, "-I", str(SCRIPTS / "apple-no-core-spec.py"),
                                     "--root", str(root), "--output-dir", str(spec.parent)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            import re
            changed = re.sub(r"(?m)^      - package: PersonaCore\n", "", spec.read_text())
            self.assertEqual(changed.count("- package: PersonaCore"), 0)
            self.assertIn("name: PersonaReferenceNoCore", changed)
            manifest = json.loads((spec.parent / "manifest.json").read_text())
            self.assertEqual(manifest["removed"], [
                {"target": "Persona", "dependency": "PersonaCore"},
                {"target": "PersonaTests", "dependency": "PersonaCore"}])
            self.assertTrue(manifest["production_project_unchanged_after_generation"])
            self.assertEqual((root / "apple" / "project.yml").read_bytes(), original)

    def test_no_core_refuses_to_overwrite(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / "apple").mkdir()
            (root / "apple" / "project.yml").write_text("targets: {}\n")
            (root / "apple" / "reference.yml").write_text("owned evidence")
            result = subprocess.run([sys.executable, "-I", str(SCRIPTS / "apple-no-core-spec.py"),
                                     "--root", str(root), "--output-dir", str(root / "apple" / "reference.yml")],
                                    capture_output=True, text=True)
            self.assertEqual(result.returncode, 2)
            self.assertEqual((root / "apple" / "reference.yml").read_text(), "owned evidence")

    def test_package_comparison_preserves_compressed_distinction(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            apps = {}
            for name, content in (("with-core", b"persona-core-reference" * 10), ("without-core", b"native")):
                app = root / (name + ".app")
                app.mkdir()
                (app / "Persona").write_bytes(content)
                apps[name] = app
            output = root / "evidence"
            tools = root / "tools"
            tools.mkdir()
            (tools / "xcrun").write_text("#!/bin/sh\nprintf 'diagnostic\\n'\n")
            (tools / "ditto").write_text('#!/bin/sh\ninput=""\noutput=""\nfor arg in "$@"; do input="$output"; output="$arg"; done\nprintf "compressed-%s" "$input" > "$output"\n')
            (tools / "xcrun").chmod(0o755)
            (tools / "ditto").chmod(0o755)
            environment = dict(os.environ, PATH=str(tools) + os.pathsep + os.environ["PATH"])
            result = subprocess.run([sys.executable, "-I", str(SCRIPTS / "apple-package-report.py"),
                                     "--with-core", str(apps["with-core"]), "--without-core", str(apps["without-core"]),
                                     "--output", str(output)], capture_output=True, text=True, env=environment)
            self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
            report = json.loads((output / "package-size.json").read_text())
            self.assertEqual(report["store_download_size"], "not_measured")
            self.assertEqual(report["phase1_download_gate_status"], "open")
            self.assertEqual(report["delta_bytes"]["logical_payload_bytes"], len(b"persona-core-reference" * 10) - len(b"native"))
            self.assertNotEqual(report["delta_bytes"]["logical_payload_bytes"], report["delta_bytes"]["compressed_zip_bytes"])
            self.assertEqual(report["with_core"]["files"][0]["sha256"], hashlib.sha256(b"persona-core-reference" * 10).hexdigest())

    def test_no_core_build_log_rejects_core_linkage(self):
        script = (ROOT / "native/app-core/scripts/apple-size-reference.sh").read_text()
        guard = script[script.index("if grep -E -- '-lpersona_core|libpersona_core'"):script.index("grep -n -A4 -B4 'Ld .*Persona.app/Persona normal'")]
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            report = root / "report"
            report.mkdir()
            log = root / "without-build.log"
            command = "set -e; OUT=" + str(root) + "; " + guard
            for content, expected in (("clang -lpersona_core -o Persona", 1), ("clang -o Persona", 0)):
                with self.subTest(content=content):
                    log.write_text(content + "\n")
                    result = subprocess.run(["bash", "-c", command], capture_output=True, text=True)
                    self.assertEqual(result.returncode, expected, result.stderr)

    def test_size_wrapper_generates_distinct_projects_and_payloads(self):
        script = (ROOT / "native/app-core/scripts/apple-size-reference.sh").read_text()
        self.assertIn('xcodegen generate -q --spec "$ROOT/apple/project.yml" --project "$OUT/with-core"', script)
        self.assertIn('xcodebuild build -project "$OUT/with-core/Persona.xcodeproj"', script)
        self.assertIn('xcodebuild build -project "$OUT/no-core/PersonaReferenceNoCore.xcodeproj"', script)
        self.assertIn('--with-core "$OUT/with-dd/Build/Products/$CONFIGURATION-iphoneos/Persona.app"', script)
        self.assertIn('--without-core "$OUT/without-dd/Build/Products/$CONFIGURATION-iphoneos/Persona.app"', script)
        self.assertIn("with-core and without-core payload paths must be distinct",
                      (SCRIPTS / "apple-package-report.py").read_text())

    def test_package_rejects_existing_output(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            output = root / "existing"
            output.mkdir()
            result = subprocess.run([sys.executable, "-I", str(SCRIPTS / "apple-package-report.py"),
                                     "--with-core", str(root), "--without-core", str(root), "--output", str(output)],
                                    capture_output=True, text=True)
            self.assertEqual(result.returncode, 2)
            self.assertIn("already exists", result.stderr)


if __name__ == "__main__":
    unittest.main()
