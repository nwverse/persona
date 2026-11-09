#!/usr/bin/env python3
"""Isolated synthetic Apple turn-folding fixtures; never connects to a real hub.

PORT=<unused port> python3 scripts/mock/turn_folding_hub.py
AppleTurnFoldingTests opens the running, completed and compacted assistant chats.
"""
import os
from pathlib import Path
import runpy
import signal
import sys
from http.server import ThreadingHTTPServer


def configure(mock):
    it, tool = mock["it"], mock["tool"]
    states = [(mock["ASK1"], "running"), (mock["ASK2"], "completed"), (mock["ASK3"], "compacted")]
    for sid, state in states:
        rows = [it("user", "text", 2, text="检查示例变更，并汇报最终结果。"),
                it("assistant", "text", 1.9, text="先读取示例仓库。")]
        rows += tool("mcp__persona__github_read", {"repository": "example/project"}, "Synthetic repository", 1.8)
        rows += [it("assistant", "text", 1.7, text="已读取仓库，正在核对示例差异。")]
        rows += tool("mcp__persona__github_diff", {"pull_request": 42}, "Synthetic patch", 1.6)
        if state == "compacted":
            rows[0]["injected"] = {"kind": "system", "state": "supervise", "level": "triage", "line": "示例任务验收"}
            rows += [it("system", "notice", 1.5, text="上下文已压缩", compact={"trigger": "auto", "pre_tokens": 167000}),
                     it("system", "notice", 1.5, text="已从压缩的上下文继续")]
        rows += tool("mcp__persona__task_review", {"verdict": "accepted", "task_id": "todo_example"}, "Accepted synthetic change", 1.4)
        if state == "running":
            rows += [it("assistant", "text", 1.3, text="正在运行最后一项检查，工具过程已收起。")]
            rows += tool("mcp__persona__supervision_finish", {"summary": "Synthetic verification"}, "", 1.2)[:1]
        else:
            rows += tool("mcp__persona__supervision_finish", {"summary": "Synthetic verification"},
                         "Synthetic verification failed" if state == "compacted" else '{"visibility":"user"}',
                         1.2, err=state == "compacted")
            rows += [it("assistant", "text", 1.1, text="示例变更已核对完成。最终结果保留在这里，所有工具和中间说明都可以展开查看。")]
        for seq, row in enumerate(rows, 1):
            row["seq"] = seq
        mock["ITEMS"][sid] = rows
        mock["TASK_FEED"][sid] = []
        session = next(s for s in mock["ASSISTANT"] if s["session_id"] == sid)
        session.update(title="", state="working" if state == "running" else "idle",
                       status="working" if state == "running" else "idle", last_text=rows[-1].get("text", ""),
                       rev=2000, total_items=len(rows), seen_at=mock["ago"](minutes=0))
    # Reuse the same trace as a Dev conversation, without supervision-only presentation.
    agent = next(s for s in mock["SESSIONS"] if s["session_id"] == mock["REVIEW"])
    agent.update(state="working", status="working", last_text="Synthetic running narration", rev=2000)
    mock["ITEMS"][mock["REVIEW"]] = [dict(row) for row in mock["ITEMS"][mock["ASK1"]]]

    # Match production FoldWork's retained visibility evidence, absent from the generic mock.
    original_fold = mock["fold_work"]

    def fold_work(items):
        folded = original_fold(items)
        results = {row.get("tool_use_id"): row for row in items if row["kind"] == "tool_result"}
        for row in folded:
            if row["kind"] != "fold":
                continue
            accepted = any(use.get("tool_name") == "mcp__persona__task_review"
                           and use.get("tool_input", {}).get("verdict") == "accepted"
                           and use.get("tool_use_id") in results
                           and not results[use["tool_use_id"]].get("is_error")
                           for use in items if row["seq"] <= use["seq"] <= row["fold"]["through_seq"])
            if accepted:
                row["fold"]["supervision_user"] = True
        return folded

    mock["H"].do_GET.__globals__["fold_work"] = fold_work


def main():
    mock = runpy.run_path(str(Path(__file__).with_name("mock_hub.py")))
    configure(mock)
    server = ThreadingHTTPServer(("127.0.0.1", int(os.environ.get("PORT", "18765"))), mock["H"])
    server.daemon_threads = True
    mock["endpoint"].__globals__["PORT"] = server.server_port
    mock["ENDPOINTS"].append(mock["endpoint"]("127.0.0.1", server.server_port, "http", "lan"))

    def stop(*_):
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, stop)
    print("RESULT: synthetic turn-folding mock listening on %d" % server.server_port, flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
        print("RESULT: synthetic turn-folding mock stopped", flush=True)


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError) as error:
        print("RESULT: synthetic turn-folding mock failed: %s" % error, file=sys.stderr)
        sys.exit(1)
