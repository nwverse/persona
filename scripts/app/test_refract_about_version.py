#!/usr/bin/env python3
"""Refract source archive and fail-closed input regressions; run with python3 -I."""
import gzip
import contextlib
import importlib.util
import io
from pathlib import Path
import subprocess
import sys
import tempfile
import types
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location(
    "refract_about_version", Path(__file__).with_name("refract_about_version.py"))
pilot = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(pilot)
refract = pilot


def archive_with_gzip_os(os_byte):
    compressed = bytearray(gzip.compress(b"canonical tar", compresslevel=9, mtime=0))
    compressed[9] = os_byte
    packaging = types.SimpleNamespace(
        source_files=lambda commit: ["About.swift"],
        run=lambda command: "100644 blob 0000000000000000000000000000000000000000\tAbout.swift",
        run_bytes=lambda command: b"source",
        deterministic_tar=lambda prefix, entries: b"canonical tar",
        ARCHIVE_EPOCH=0,
    )
    with tempfile.TemporaryDirectory() as temporary:
        output = Path(temporary) / "source.tar.gz"
        with mock.patch.object(refract.importlib.util, "spec_from_file_location") as spec, \
                mock.patch.object(refract.importlib.util, "module_from_spec", return_value=packaging), \
                mock.patch.object(refract.gzip, "compress", return_value=bytes(compressed)):
            spec.return_value.loader = mock.Mock()
            refract.canonical_source_archive(Path("unused"), {"commit": "abc"}, output)
        return output.read_bytes()


class RefractAboutVersionTests(unittest.TestCase):
    def test_canonical_archive_normalizes_gzip_os_byte(self):
        archives = [archive_with_gzip_os(os_byte) for os_byte in (0, 3, 255)]
        self.assertEqual(archives, [archives[1]] * 3)
        self.assertEqual(archives[0][9], 3)
        self.assertEqual([gzip.decompress(archive) for archive in archives], [b"canonical tar"] * 3)


DIAGNOSTIC = (
    "UnsupportedAboutControl.swift:5:9: error [missingComponentMapping]: The component "
    "`UnsupportedAboutControlHost` has no Android mapping. "
    "Suggestion: Declare its typed target in `android.components.UnsupportedAboutControlHost`.\n"
)


class UnsupportedInputTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="persona-refract-about-test-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)

    def test_rejected_input_prevents_the_entrypoint_from_replacing_reviewed_kotlin(self):
        generated = self.root / "AboutVersionRow.kt"
        generated.write_bytes(b"reviewed Kotlin\n")
        calls = []

        def reject_mixed_input(cli, mode, android_out, harmony_out, sources=(pilot.SOURCE,)):
            calls.append((mode, sources))
            if len(sources) == 2:
                raise subprocess.CalledProcessError(1, str(cli), stderr=DIAGNOSTIC)
            # A silent accepted negative followed by a valid generate must not publish the valid row.
            if mode == "generate" and [call[0] for call in calls].count("generate") == 1:
                generated.write_bytes(b"unreviewed replacement\n")
                return
            raise subprocess.CalledProcessError(1, str(cli), stderr=DIAGNOSTIC)

        argv = ["refract_about_version.py", "generate", "--cli", str(self.root / "refract")]
        (self.root / "refract").touch()
        stderr = io.StringIO()
        with mock.patch.object(pilot, "GENERATED", generated), \
                mock.patch.object(pilot, "run", reject_mixed_input), \
                mock.patch.object(pilot, "validate_lock", return_value={}), \
                mock.patch.object(sys, "argv", argv), contextlib.redirect_stderr(stderr):
            self.assertEqual(pilot.main(), 1)
        self.assertEqual(generated.read_bytes(), b"reviewed Kotlin\n")
        self.assertEqual([mode for mode, _ in calls], ["check", "generate", "generate"])
        self.assertEqual([source.name for _, sources in calls for source in sources],
                         ["AboutVersionRow.swift", "UnsupportedAboutControl.swift"] * 2 +
                         ["AboutVersionRow.swift"])
        self.assertIn("UnsupportedAboutControl.swift:5:9", stderr.getvalue())
        self.assertIn("UnsupportedAboutControlHost", stderr.getvalue())
        self.assertIn("Suggestion: Declare its typed target", stderr.getvalue())

    def test_silent_acceptance_missing_diagnostics_and_partial_output_all_fail(self):
        for behavior in ("silent success", "missing diagnostics", "partial output"):
            with self.subTest(behavior=behavior), mock.patch.object(pilot, "GENERATED", self.root / "generated.kt"):
                (self.root / "generated.kt").write_bytes(b"reviewed\n")

                def broken_generator(cli, mode, android_out, harmony_out, sources=(pilot.SOURCE,)):
                    if behavior == "partial output":
                        android_out.mkdir(parents=True)
                        (android_out / "AboutVersionRow.kt").write_text("partial sibling")
                    if behavior != "silent success":
                        detail = DIAGNOSTIC if behavior == "partial output" else "refract: failed\n"
                        raise subprocess.CalledProcessError(1, str(cli), stderr=detail)

                with mock.patch.object(pilot, "run", broken_generator):
                    with self.assertRaises(ValueError) as caught:
                        pilot.check_unsupported_input(self.root / "refract", self.root / behavior)
                self.assertIsInstance(caught.exception, ValueError)


if __name__ == "__main__":
    unittest.main()
