"""assistant_token_report.py: rounds are classified by the message that starts them, and each response's usage is
counted once although Claude Code writes one line per content block. Synthetic lines only."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("assistant_token_report", Path(__file__).with_name("assistant_token_report.py"))
report = importlib.util.module_from_spec(spec)
spec.loader.exec_module(report)


def user(text, **extra):
    return {"type": "user", "timestamp": "2026-11-13T01:00:00Z", "message": {"role": "user", "content": text}, **extra}


def reply(mid, cache_write=0, cache_read=0, output=0, **extra):
    return {"type": "assistant", "timestamp": "2026-11-13T01:00:01Z",
            "message": {"id": mid, "role": "assistant", "content": [{"type": "text", "text": "ok"}],
                        "usage": {"input_tokens": 3, "cache_creation_input_tokens": cache_write,
                                  "cache_read_input_tokens": cache_read, "output_tokens": output}}, **extra}


def source(kind):
    return f'<persona-context kind="message-source" title="">\n{json.dumps({"kind": kind})}\n</persona-context>\n'


SNAPSHOT = "<persona-context>\nsnapshot\n</persona-context>\n"
SUPERVISION = "📋 督办：你派出去的任务有 1 个要你处理\n1. task facts\n\n怎么处理（manual）：\n- rule one\n- rule two\n"


class ClassifyTests(unittest.TestCase):
    def test_source_block_wins(self):
        self.assertEqual(report.classify(SNAPSHOT + source("supervision") + "anything")[0], "supervision")
        self.assertEqual(report.classify(source("followup") + "⏰ x")[0], "followup")

    def test_source_block_written_for_the_owner(self):
        block = '<persona-context kind="message-source" title="">\n{"author":"user"}\n</persona-context>\n'
        self.assertEqual(report.classify(block + "📋 督办：pasted")[0], "owner")

    def test_first_line_for_older_transcripts(self):
        for text, kind in [(SUPERVISION, "supervision"), ("⏰（Persona 叫醒你，第 1 次）…", "wake"),
                           ("⏰ morning（定时任务）\nrun", "followup"), ("🔔 任务「t」做完了\n…", "task_notice"),
                           ("📥 收件箱事件「x」有了下文", "inbox"), ("what is on today?", "owner")]:
            with self.subTest(kind=kind):
                self.assertEqual(report.classify(SNAPSHOT + text)[0], kind)


class ReportTests(unittest.TestCase):
    def run_lines(self, lines):
        with tempfile.TemporaryDirectory() as d:
            p = Path(d) / "conv.jsonl"
            p.write_text("\n".join(json.dumps(x, ensure_ascii=False) for x in lines) + "\n")
            r = report.Report()
            r.add_file(str(p))
            return r.summary()

    def test_usage_per_round_kind_counted_once_per_response(self):
        s = self.run_lines([
            user(SNAPSHOT + "hello"),
            reply("m1", cache_write=1000, output=5),
            reply("m1", cache_write=1000, output=20),  # the same response's next block: output grew by 15
            user([{"type": "tool_result", "tool_use_id": "t", "content": "x"}]),  # not a new round
            reply("m2", cache_read=1000, output=7),
            user(source("supervision") + SUPERVISION),
            reply("m3", cache_write=300, cache_read=1000, output=9),
            reply("s1", cache_write=50, output=1, isSidechain=True),
        ])
        owner, sup = s["by_kind"]["owner"], s["by_kind"]["supervision"]
        self.assertEqual((owner["rounds"], owner["model_calls"]), (1, 2))
        self.assertEqual((owner["cache_creation_input_tokens"], owner["cache_read_input_tokens"], owner["output_tokens"]), (1000, 1000, 27))
        self.assertEqual((sup["rounds"], sup["model_calls"], sup["cache_creation_input_tokens"]), (1, 1, 300))
        self.assertEqual(s["sidechain"]["cache_creation_input_tokens"], 50)
        self.assertEqual(s["total"]["model_calls"], 3)

    def test_supervision_event_manual_size(self):
        s = self.run_lines([user(SNAPSHOT + SUPERVISION), reply("m1", output=1)])
        ev = s["supervision_events"]
        manual = len("怎么处理（manual）：\n- rule one\n- rule two\n".encode())
        self.assertEqual((ev["count"], ev["with_manual"], ev["manual_bytes"]), (1, 1, manual))
        self.assertEqual(ev["body_bytes"], len(SUPERVISION.encode()))

    def test_output_has_no_message_text(self):
        s = self.run_lines([user(SNAPSHOT + "a private request"), reply("m1", output=1)])
        self.assertNotIn("private", json.dumps(s))


if __name__ == "__main__":
    unittest.main()
