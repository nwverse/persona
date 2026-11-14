#!/usr/bin/env python3
"""Check archived #6871 observations and summary numbers against their unmodified raw JSON."""
import hashlib
import json
import pathlib
import statistics
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
RAW = ROOT / "docs/archive/apple/reference-measurements-6871/raw"
ARCHIVE = RAW.parent


def read(name):
    return json.loads((RAW / name).read_text())


def summary(values):
    ordered = sorted(values)
    return [ordered[(len(ordered) - 1) // 2], ordered[int((len(ordered) - 1) * .99)], ordered[-1]]


class ReferenceEvidenceTests(unittest.TestCase):
    def test_file_hashes_match_the_original_exports(self):
        expected = {
            "native-launch.json": "175edc26496d3f66ae2635dfcef3d1913208742643ec2774c61a3ec04a1ab7cd",
            "native-stream.json": "d7b34a34c93e1e16946754bf4ce4506c6a85e714042edad84e95fd5ad033f3bb",
            "native-glide.json": "58decf4884dcfd0dfea9b963dc4625223024c7fe7fd3af55cb81362a5df5edaa",
            "native-memory.json": "1652c9314ceb1d197d0fcdaf13535dc6a1b6af23e4a7919527137363b9bb2c53",
            "core-boundary.json": "5370253407705272afd4b9366bfb8064d1a06927192dabcbc92492de100954d3",
            "core-large-page.json": "7133b1837d2aec509e4bb0931f527bceff5bfb341565cd00a4b47075d831c62c",
            "core-lifecycle.json": "dde2e36626ded9abab4dde7760bb759cdec64ab5a0a9151d17c45c644f731b99",
        }
        actual = {path.name: hashlib.sha256(path.read_bytes()).hexdigest() for path in RAW.glob("*.json")}
        self.assertEqual(expected, actual)

    def test_launch_has_one_warmup_and_three_tagged_runs(self):
        rows = read("native-launch.json")
        self.assertEqual([row["warm_up"] for row in rows], [True, False, False, False])
        self.assertEqual(len({row["run_id"] for row in rows}), 4)
        self.assertEqual([row["action"] for row in rows], ["launch"] * 4)
        self.assertEqual(statistics.median(row["view_appearance_ms"] for row in rows[1:]), 1328.5042501520365)

    def test_stream_and_glide_have_real_work_and_explicit_warmups(self):
        for name, action in (("native-stream.json", "stream"), ("native-glide.json", "glide")):
            rows = read(name)
            self.assertEqual([row["warm_up"] for row in rows], [True, False, False, False])
            self.assertEqual(len({row["run_id"] for row in rows}), 4)
            self.assertTrue(all(row["action"] == action and row["frames"] > 0 for row in rows))
            if action == "stream":
                self.assertTrue(all(row["text_changes"] > 0 for row in rows))
            else:
                self.assertTrue(all(row["scroll_offset_delta_pt"] > 24 for row in rows))
                self.assertTrue(all(row["sample_source"] == "app-list-glide" for row in rows))

    def test_native_memory_is_ordered_and_has_distinct_metrics(self):
        rows = read("native-memory.json")
        self.assertEqual([row["sequence"] for row in rows], list(range(1, 13)))
        self.assertEqual(rows[-1]["phys_footprint_bytes"] - rows[0]["phys_footprint_bytes"], 606208)
        self.assertEqual(rows[-1]["resident_size_bytes"] - rows[0]["resident_size_bytes"], 4128768)

    def test_every_binding_metric_retains_one_thousand_samples(self):
        report = read("core-boundary.json")
        self.assertEqual(report["warmups_per_metric"], 100)
        self.assertEqual(report["samples_per_metric"], 1000)
        self.assertTrue(report["worker_flush_is_outside_timed_spans"])
        groups = {}
        for row in report["raw_samples"]:
            self.assertTrue(row["measured_on_main_thread"])
            groups.setdefault(row["metric"], []).append(row["value_us"])
        expected = {
            "core_dispatch_encode_and_queue": [3.625, 6.75, 15.5],
            "core_drain_decode_apply": [3.125, 6.958, 15.791],
            "core_patch_json_decode_item_apply": [6.625, 19.375, 73.167],
            "native_json_decode_store_apply": [7.334, 8.209, 12.709],
        }
        for metric, values in groups.items():
            self.assertEqual(len(values), 1000, metric)
            if metric in expected:
                self.assertEqual(summary(values), expected[metric], metric)
        self.assertEqual(report["patch_op_counts"], [1] * 1000)
        large = read("core-large-page.json")
        self.assertEqual(len(large["samples"]), 1000)
        self.assertEqual(summary(large["samples"]), [83.667, 183.667, 563.333])
        self.assertEqual(large["patch_op_count"], 100)

    def test_core_lifecycle_has_ten_post_warmup_cycles(self):
        rows = read("core-lifecycle.json")
        self.assertEqual(rows["warmup_cycles"], 1)
        self.assertEqual(rows["cycles"], 10)
        self.assertEqual([row["cycle"] for row in rows["samples"]], list(range(1, 11)))
        for row in rows["samples"]:
            self.assertEqual(row["phys_footprint_bytes"] - rows["baseline_phys_footprint_bytes"],
                             row["delta_from_warmed_bytes"])
        self.assertEqual(rows["final_delta_bytes"], 16384)
    def test_launch_metric_and_device_provenance_are_retained(self):
        report = json.loads((ARCHIVE / "launch-report.json").read_text())
        test = report["tests"]["PerfReferenceTests/testNativeFirstFrame()"]
        metric = test["metrics"]["com.apple.dt.XCTMetric_ApplicationLaunch-AppLaunch.duration"]
        self.assertEqual(metric["values"], [5.26995975, 5.611702416, 6.161564292])
        manifest = json.loads((ARCHIVE / "launch-export-manifest.json").read_text())
        self.assertEqual(manifest[0]["attachments"][0]["deviceId"], "58B70B19-21DA-4051-B017-0D828579958A")
        self.assertIn("source_dirty: yes", (ARCHIVE / "launch-environment.txt").read_text())
        binding = json.loads((ARCHIVE / "binding-export-manifest.json").read_text())
        self.assertEqual(len(binding), 3)
        self.assertTrue(all(item["attachments"][0]["deviceId"] == "58B70B19-21DA-4051-B017-0D828579958A"
                            for item in binding))

    def test_package_reports_reproduce_payload_not_store_size(self):
        original = json.loads((ARCHIVE / "package-original.json").read_text())
        recomputed = json.loads((ARCHIVE / "package-recomputed.json").read_text())
        self.assertEqual(original["delta_bytes"], recomputed["delta_bytes"])
        self.assertEqual(original["delta_bytes"], {"logical_payload_bytes": 763346, "allocated_payload_bytes": 761856,
                                                    "executable_bytes": 763344, "compressed_zip_bytes": 355744})
        for variant in ("with_core", "without_core"):
            self.assertEqual(original[variant]["files"], recomputed[variant]["files"])
            self.assertEqual(original[variant]["executable_sha256"], recomputed[variant]["executable_sha256"])
        self.assertEqual(original["compressed"]["with_core"]["sha256"],
                         "29df55283674ada57586576bee80acdda16f1eee0f7418f33991de123f7a7cc0")
        self.assertEqual(original["compressed"]["without_core"]["sha256"],
                         "aae48553d05749052092f133cc458be71cdbe9e8ea6e47a29bbe5c06622ceef3")
        self.assertNotEqual(original["xcframework_provenance"], recomputed["xcframework_provenance"])
        self.assertEqual(original["store_download_size"], "not_measured")
        self.assertEqual(original["phase1_download_gate_status"], "open")


if __name__ == "__main__":
    unittest.main(verbosity=2)
