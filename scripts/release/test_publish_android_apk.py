import argparse
import concurrent.futures
import gzip
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

import android_apk_atomic as atomic
import publish_android_apk as publisher

CERT = "a" * 64
BADGING = "package: name='test.persona' versionCode='42' versionName='1.2'\nsdkVersion:'26'\n"
SIGNING = "Number of signers: 1\nSigner #1 certificate SHA-256 digest: " + CERT + "\n"


class SignerCertificateTests(unittest.TestCase):
    def test_legacy_output(self):
        output = "Verifies\n" + SIGNING + "Signer #1 public key SHA-256 digest: " + "b" * 64 + "\n"
        self.assertEqual(publisher.parse_signer_certificate(output), CERT)

    def test_sdk37_v3_output(self):
        output = ("Verifies\nVerified using v3 scheme (APK Signature Scheme v3): true\n"
                  "Number of signers: 1\nV3.0 Signer: certificate DN: CN=Release\n"
                  "V3.0 Signer: certificate SHA-256 digest: " + CERT.upper() + "\n"
                  "V3.0 Signer: public key SHA-256 digest: " + "b" * 64 + "\n")
        self.assertEqual(publisher.parse_signer_certificate(output), CERT)

    def test_rejects_multiple_missing_duplicate_or_malformed_signers(self):
        invalid = [SIGNING.replace("signers: 1", "signers: 2"),
                   SIGNING.replace("Number of signers: 1\n", ""),
                   "Number of signers: 1\n" + SIGNING,
                   SIGNING + SIGNING.splitlines()[1] + "\n",
                   SIGNING + "V3.0 Signer: certificate SHA-256 digest: " + "b" * 64 + "\n",
                   SIGNING.replace(CERT, "g" * 64), SIGNING.replace(CERT, "a" * 63),
                   SIGNING.replace(CERT, "a" * 65), SIGNING.replace("Signer #1", "Signer #2")]
        for output in invalid:
            with self.subTest(output=output):
                with self.assertRaises(ValueError):
                    publisher.parse_signer_certificate(output)


class AtomicPublicationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.directory = self.root / "apps" / "android"

    def stage(self, version, payload=None):
        stage = Path(tempfile.mkdtemp(dir=self.root))
        apk = stage / "app.apk"
        apk.write_bytes(payload or f"apk-{version}".encode())
        release = dict(version_code=version, sha256=atomic.digest_file(apk), size=apk.stat().st_size)
        apk.rename(stage / (release["sha256"] + ".apk"))
        (stage / "latest.json").write_text(json.dumps(release))
        return stage, release

    def current(self):
        release = json.loads((self.directory / "latest.json").read_text())
        self.assertEqual(atomic.digest_file(self.directory / (release["sha256"] + ".apk")), release["sha256"])
        return release

    def test_stale_and_conflicting_releases_preserve_current(self):
        stage, release = self.stage(42)
        atomic.publish(self.directory, stage)
        for version, data, reason in [(41, None, "stale"), (42, b"different", "different APK")]:
            stage, _ = self.stage(version, data)
            with self.assertRaisesRegex(ValueError, reason):
                atomic.publish(self.directory, stage)
            self.assertEqual(self.current(), release)

    def test_idempotent_and_retains_previous_apk(self):
        stage, previous = self.stage(42)
        atomic.publish(self.directory, stage)
        stage, _ = self.stage(42)
        self.assertEqual(atomic.publish(self.directory, stage), "already published")
        stage, latest = self.stage(43, b"stored dex " * 4096)
        atomic.publish(self.directory, stage)
        self.assertEqual(self.current(), latest)
        self.assertTrue((self.directory / (previous["sha256"] + ".apk")).is_file())
        # Each APK keeps a gzip copy that decodes to it, for downloads that take gzip; the old one stays with its APK.
        for release in (previous, latest):
            apk = self.directory / (release["sha256"] + ".apk")
            self.assertEqual(gzip.decompress((self.directory / (apk.name + ".gz")).read_bytes()), apk.read_bytes())
        self.assertLess((self.directory / (latest["sha256"] + ".apk.gz")).stat().st_size, latest["size"] // 10)
        self.assertEqual(sorted(p.name for p in self.directory.iterdir()), sorted(
            [".publish.lock", "latest.json"] + [r["sha256"] + s for r in (previous, latest) for s in (".apk", ".apk.gz")]))

    def test_failed_manifest_replace_preserves_current(self):
        stage, current = self.stage(42)
        atomic.publish(self.directory, stage)
        stage, _ = self.stage(43)
        replace = atomic.os.replace

        def fail_manifest(source, destination):
            if Path(destination).name == "latest.json":
                raise OSError("injected disk failure")
            return replace(source, destination)

        with mock.patch.object(atomic.os, "replace", side_effect=fail_manifest):
            with self.assertRaisesRegex(OSError, "disk failure"):
                atomic.publish(self.directory, stage)
        self.assertEqual(self.current(), current)

    def test_corrupt_upload_preserves_current(self):
        stage, current = self.stage(42)
        atomic.publish(self.directory, stage)
        stage, release = self.stage(43)
        (stage / (release["sha256"] + ".apk")).write_bytes(b"corrupt")
        with self.assertRaisesRegex(ValueError, "hash or size"):
            atomic.publish(self.directory, stage)
        self.assertEqual(self.current(), current)

    def test_concurrent_processes_keep_highest_version(self):
        stages = [self.stage(version)[0] for version in range(1, 17)]

        def publish(stage):
            return subprocess.run(["python3", atomic.__file__, "--directory", str(self.directory),
                                   "--stage", str(stage)], capture_output=True, text=True)

        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as executor:
            results = list(executor.map(publish, reversed(stages)))
        for result in results:
            if result.returncode:
                self.assertIn("stale version_code", result.stderr)
        self.assertEqual(self.current()["version_code"], 16)


class ApkValidationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.apk = Path(self.temp.name) / "test.apk"
        self.apk.write_bytes(b"test APK bytes")
        self.args = publisher.parser().parse_args([str(self.apk), "--expected-package", "test.persona",
                                                   "--expected-cert-sha256", CERT])

    def inspect(self, signing=SIGNING):
        with mock.patch.object(publisher, "tool", side_effect=lambda name: name):
            with mock.patch.object(publisher, "run", side_effect=[BADGING, signing]):
                return publisher.inspect_apk(self.apk, self.args)

    def test_valid_apk_records_verified_identity(self):
        self.args.source_sha = "b" * 40
        result = self.inspect()
        self.assertEqual(result["version_code"], 42)
        self.assertEqual(result["certificate_sha256"], CERT)
        self.assertEqual(result["source_sha"], "b" * 40)
        self.assertEqual(result["sha256"], atomic.digest_file(self.apk))

    def test_sdk37_minimum_sdk_badging_is_supported(self):
        with mock.patch.object(publisher, "tool", side_effect=lambda name: name):
            with mock.patch.object(publisher, "run", side_effect=[BADGING.replace("sdkVersion:'26'", "minSdkVersion:'30'"), SIGNING]):
                result = publisher.inspect_apk(self.apk, self.args)
        self.assertEqual(result["min_sdk"], 30)

    def test_explicit_metadata_cannot_override_actual_apk(self):
        self.args.version_code = 999
        with self.assertRaisesRegex(ValueError, "differs from actual"):
            self.inspect()

    def test_certificate_mismatch_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "certificate differs"):
            self.inspect(SIGNING.replace(CERT, "b" * 64))

    def test_package_mismatch_is_rejected(self):
        self.args.expected_package = "different.package"
        with self.assertRaisesRegex(ValueError, "package differs"):
            self.inspect()

    def test_failed_signature_verification_is_rejected(self):
        with mock.patch.object(publisher, "tool", side_effect=lambda name: name):
            with mock.patch.object(publisher, "run", side_effect=[BADGING, subprocess.CalledProcessError(1, "apksigner")]):
                with self.assertRaises(subprocess.CalledProcessError):
                    publisher.inspect_apk(self.apk, self.args)

    def test_upload_archive_and_remote_commit_end_to_end(self):
        directory = Path(self.temp.name) / "hub" / "apps" / "android"
        self.args.hub_ssh = "test-hub"
        self.args.remote_dir = str(directory)
        actual_run = subprocess.run

        def local_ssh(command, **kwargs):
            self.assertEqual(command[:3], ["ssh", "--", "test-hub"])
            return actual_run(["sh", "-c", command[3]], **kwargs)

        with mock.patch.object(publisher, "tool", side_effect=lambda name: name):
            with mock.patch.object(publisher, "run", side_effect=[BADGING, SIGNING]):
                with mock.patch.object(publisher.subprocess, "run", side_effect=local_ssh):
                    publisher.publish(self.args)
        release = json.loads((directory / "latest.json").read_text())
        self.assertEqual(release["version_code"], 42)
        self.assertEqual(atomic.digest_file(directory / (release["sha256"] + ".apk")), release["sha256"])
        self.assertEqual(list(directory.glob(".upload-*")), [])

    def test_ssh_transport_requires_nonempty_argv(self):
        for value in ('[]', '{}', '"ssh"', '[1]', '[""]'):
            with self.assertRaises(argparse.ArgumentTypeError):
                publisher.command_argv(value)
        self.assertEqual(publisher.command_argv('["/private/adapter", "--pinned"]'),
                         ["/private/adapter", "--pinned"])

    def test_remote_directory_is_scoped(self):
        self.args.hub_ssh = "hub"
        self.args.remote_dir = "/root/.persona/bin"
        with self.assertRaisesRegex(ValueError, "ending in /apps/android"):
            publisher.destination(self.args)


if __name__ == "__main__":
    unittest.main()
