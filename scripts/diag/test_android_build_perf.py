"""android_build_perf.py's arithmetic: the histogram gfxinfo prints, the percentiles and shares read from it, and the
links a release build is opened with."""
import importlib.util
import pathlib
import unittest

spec = importlib.util.spec_from_file_location("android_build_perf", pathlib.Path(__file__).with_name("android_build_perf.py"))
perf = importlib.util.module_from_spec(spec)
spec.loader.exec_module(perf)

GFXINFO = """Total frames rendered: 10
Janky frames: 2 (20.00%)
50th percentile: 9ms
HISTOGRAM: 5ms=1 6ms=0 8ms=4 16ms=3 32ms=1 150ms=1
50th gpu percentile: 3ms
"""


class AndroidBuildPerfTest(unittest.TestCase):
    def test_reads_the_histogram_line_only(self):
        self.assertEqual(perf.histogram(GFXINFO), dict([(5, 1), (6, 0), (8, 4), (16, 3), (32, 1), (150, 1)]))
        self.assertEqual(perf.histogram("Total frames rendered: 0"), dict())

    def test_percentiles_are_the_bucket_they_fall_in(self):
        found = perf.histogram_stats(perf.histogram(GFXINFO))
        self.assertEqual(found["frames"], 10)
        self.assertEqual((found["p50"], found["p90"], found["p99"]), (8, 32, 150))

    def test_shares_count_buckets_above_the_limit(self):
        found = perf.histogram_stats(perf.histogram(GFXINFO))
        # 16 ms is not over 16; 32 ms and 150 ms are
        self.assertEqual(found["over_16ms_percent"], 20.0)
        self.assertEqual(found["over_32ms_percent"], 10.0)

    def test_no_frames_is_no_statistics(self):
        self.assertIsNone(perf.histogram_stats(dict()))
        self.assertIsNone(perf.histogram_stats(dict([(8, 0)])))

    def test_runs_add_up_bucket_by_bucket(self):
        first = dict(histogram=dict([(8, 2), (16, 1)]))
        second = dict(histogram=dict([(8, 3), (32, 1)]))
        self.assertEqual(perf.merged([first, second]), dict([(8, 5), (16, 1), (32, 1)]))

    def test_links_open_the_list_and_a_conversation(self):
        self.assertEqual(perf.scenarios(None, None), dict(agents="persona://open?tab=agents"))
        both = perf.scenarios("server/s-1", None)
        self.assertEqual(both["chat"], "persona://open?tab=agents&machine=server&session=s-1")
        self.assertEqual(list(perf.scenarios("server/s-1", ["chat"])), ["chat"])


if __name__ == "__main__":
    unittest.main()
