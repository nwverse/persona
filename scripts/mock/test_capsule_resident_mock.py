"""Exercise resident-capsule evidence through real HTTP and WebSocket connections."""
import base64
from datetime import datetime, timezone
import json
import os
import socket
import struct
import time
import unittest
from urllib.parse import urlencode
from test_mock_hub_mesh import Mock


class ResidentSocket:
    def __init__(self, hub, delta=False):
        self.socket = socket.create_connection(("127.0.0.1", hub.port), timeout=3)
        self.stream = self.socket.makefile("rb")
        key = base64.b64encode(os.urandom(16)).decode()
        path = "/api/v1/ws" + ("?features=sessions_delta" if delta else "")
        self.socket.sendall((f"GET {path} HTTP/1.1\r\nHost: 127.0.0.1:{hub.port}\r\nUpgrade: websocket\r\n"
                             f"Connection: Upgrade\r\nSec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n\r\n").encode())
        if b" 101 " not in self.stream.readline():
            raise AssertionError("WebSocket upgrade failed")
        while self.stream.readline() not in (b"\r\n", b""):
            pass
        self.hello = self.until(lambda frame: frame.get("type") == "hello")

    def frame(self):
        head = self.stream.read(2)
        if len(head) != 2:
            raise AssertionError("WebSocket closed before the expected frame")
        length = head[1] & 0x7F
        if length == 126:
            length = int.from_bytes(self.stream.read(2), "big")
        elif length == 127:
            length = int.from_bytes(self.stream.read(8), "big")
        return head[0] & 0x0F, self.stream.read(length)

    def send_frame(self, opcode, payload):
        mask = os.urandom(4)
        n = len(payload)
        length = bytes([0x80 | n]) if n < 126 else bytes([0x80 | 126]) + struct.pack(">H", n)
        self.socket.sendall(bytes([0x80 | opcode]) + length + mask +
                            bytes(value ^ mask[index % 4] for index, value in enumerate(payload)))

    def send(self, value):
        self.send_frame(1, json.dumps(value).encode())

    def until(self, predicate, timeout=3):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            self.socket.settimeout(max(.1, deadline - time.monotonic()))
            opcode, data = self.frame()
            if opcode == 9:
                self.send_frame(10, data)
            elif opcode == 1:
                value = json.loads(data)
                if predicate(value):
                    return value
        raise AssertionError("Expected WebSocket frame was not received")

    def close(self):
        if self.socket.fileno() < 0:
            return
        try:
            self.send_frame(8, b"")
        except OSError:
            pass
        self.stream.close()
        self.socket.close()


class CapsuleResidentMockTests(unittest.TestCase):
    def setUp(self):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        self.hub = Mock(port)
        self.addCleanup(self.hub.stop)
        self.mid = "macbook"
        self.sid = "a1f3c2d4-0000-4000-8000-000000000001"

    def connect(self, delta=False):
        client = ResidentSocket(self.hub, delta)
        self.addCleanup(client.close)
        return client

    def incoming(self, mid, sid, text):
        return self.hub.get("/mock/incoming?" + urlencode(dict(mid=mid, sid=sid, text=text)))

    def sessions(self):
        return {(s["machine_id"], s["session_id"]): s for s in self.hub.get("/api/v1/sessions")["sessions"]}

    def observation(self, client):
        return next(c for c in self.hub.get("/mock/ws_clients")["clients"] if c["id"] == client.hello["client_id"])

    def wait_observation(self, client, predicate):
        deadline = time.monotonic() + 3
        while time.monotonic() < deadline:
            value = self.observation(client)
            if predicate(value):
                return value
            time.sleep(.05)
        self.fail("WebSocket observation did not update: " + repr(value))

    def test_unsubscribed_reply_updates_http_and_global_delta(self):
        before = self.sessions()
        client = self.connect(delta=True)
        client.until(lambda f: f.get("type") == "sessions" and f.get("machine_id") == self.mid)
        baseline = self.observation(client)
        started = datetime.now(timezone.utc)
        reply = self.incoming(self.mid, self.sid, "resident-unsubscribed-2124")
        item, session = reply["item"], reply["session"]
        self.assertEqual((item["role"], item["kind"], item["text"]),
                         ("assistant", "text", "resident-unsubscribed-2124"))
        self.assertGreaterEqual(datetime.fromisoformat(item["ts"].replace("Z", "+00:00")), started)
        self.assertEqual((session["state"], session["status"], session["last_text"]),
                         ("unread", "done", item["text"]))
        self.assertGreater(session["rev"], before[(self.mid, self.sid)]["rev"])
        frame = client.until(lambda f: f.get("type") == "sessions_delta" and f.get("machine_id") == self.mid)
        self.assertEqual(frame["upsert"], [session])
        after = self.sessions()
        self.assertEqual(after[(self.mid, self.sid)], session)
        self.assertEqual({k: v for k, v in before.items() if k != (self.mid, self.sid)},
                         {k: v for k, v in after.items() if k != (self.mid, self.sid)})
        items = self.hub.get(f"/api/v1/sessions/{self.mid}/{self.sid}/items")["items"]
        self.assertIn(item, items)
        observed = self.observation(client)
        self.assertTrue(observed["requested_sessions_delta"])
        self.assertGreater(observed["session_frames"], baseline["session_frames"])
        self.assertEqual(observed["item_frames"], 0)
        self.assertNotIn("token", observed)
        self.assertNotIn("query", observed)

    def test_subscribed_assistant_reply_and_seen_flow_back(self):
        main = self.hub.get("/api/v1/assistant")
        mid, sid = main["machine_id"], main["main"]
        client = self.connect()
        client.send(dict(type="subscribe", machine_id=mid, session_id=sid))
        client.until(lambda f: f.get("type") == "items" and f.get("session_id") == sid)
        reply = self.incoming(mid, sid, "resident-main-reply-2124")
        pushed = client.until(lambda f: f.get("type") == "items" and reply["item"] in f.get("items", []))
        self.assertEqual((pushed["machine_id"], pushed["session_id"]), (mid, sid))
        started = datetime.now(timezone.utc)
        client.send(dict(type="seen", machine_id=mid, session_id=sid))
        frame = client.until(lambda f: f.get("type") == "sessions" and any(
            s["session_id"] == sid and s["state"] == "idle" for s in f.get("sessions", [])))
        seen = next(s for s in frame["sessions"] if s["session_id"] == sid)
        self.assertGreaterEqual(datetime.fromisoformat(seen["seen_at"].replace("Z", "+00:00")), started)
        self.assertEqual(self.sessions()[(mid, sid)]["state"], "idle")
        self.assertIn(sid, self.hub.get("/mock/seen")["seen"])
        self.assertGreaterEqual(self.observation(client)["item_frames"], 2)

    def test_heartbeat_and_disconnect_history_are_observable(self):
        client = self.connect()
        client.send(dict(type="client_hello", features=["app_ping", "heartbeat"],
                         heartbeat=dict(interval=10, pong_timeout=5)))
        ack = client.until(lambda f: f.get("type") == "client_hello_ack")
        self.assertEqual(ack["heartbeat"], dict(interval=10, pong_timeout=5))
        client.send(dict(type="ping", id="resident-probe"))
        client.until(lambda f: f.get("type") == "pong" and f.get("id") == "resident-probe")
        # The server's negotiated minimum is ten real seconds; answer its actual control ping.
        deadline = time.monotonic() + 13
        while time.monotonic() < deadline:
            client.socket.settimeout(max(.1, deadline - time.monotonic()))
            opcode, payload = client.frame()
            if opcode == 9:
                client.send_frame(10, payload)
                break
        else:
            self.fail("Server heartbeat ping was not emitted")
        observed = self.wait_observation(client, lambda o: o["client_pings"] == 1 and o["server_pings"] >= 1 and o["client_pongs"] >= 1)
        self.assertEqual(observed["heartbeat"], dict(interval=10, pong_timeout=5))
        client.close()
        closed = self.wait_observation(client, lambda o: not o["connected"])
        self.assertGreaterEqual(closed["disconnected_at"], closed["connected_at"])

    def test_invalid_incoming_does_not_mutate_any_conversation(self):
        before = self.sessions()
        before_items = self.hub.get(f"/api/v1/sessions/{self.mid}/{self.sid}/items")
        cases = [(dict(mid=self.mid, sid=self.sid), 400),
                 (dict(mid=self.mid, sid=self.sid, text=" "), 400),
                 (dict(mid=self.mid, sid=self.sid, text="reply", extra="1"), 400),
                 (dict(mid="server", sid=self.sid, text="reply"), 404),
                 (dict(mid=self.mid, sid="missing", text="reply"), 404)]
        for query, expected in cases:
            with self.subTest(query=query):
                code, body = self.hub.get_status("/mock/incoming?" + urlencode(query))
                self.assertEqual(code, expected)
                self.assertIn("error", body)
        code, _ = self.hub.get_status("/mock/incoming?" + urlencode(dict(mid=self.mid, sid=self.sid, text="reply")) + "&text=again")
        self.assertEqual(code, 400)
        self.assertEqual(self.sessions(), before)
        self.assertEqual(self.hub.get(f"/api/v1/sessions/{self.mid}/{self.sid}/items"), before_items)

    def test_quiet_baseline_has_no_action_until_idle_session_gets_reply(self):
        before = self.sessions()
        code, _ = self.hub.get_status("/mock/capsule_quiet?extra=1")
        self.assertEqual(code, 400)
        self.assertEqual(self.sessions(), before)
        self.assertEqual(self.hub.get("/mock/capsule_quiet"), {"ok": True})
        quiet = self.sessions()
        self.assertTrue(quiet)
        self.assertTrue(all(s["state"] == "idle" and s["status"] == "idle" for s in quiet.values()))
        self.assertEqual(self.hub.get("/api/v1/interactions")["interactions"], [])
        self.assertTrue(all(item.get("done_at") for item in self.hub.get("/api/v1/inbox")["items"]))
        client = self.connect(delta=True)
        client.until(lambda f: f.get("type") == "sessions" and f.get("machine_id") == self.mid)
        reply = self.incoming(self.mid, self.sid, "resident-idle-to-unread")
        frame = client.until(lambda f: f.get("type") == "sessions_delta" and f.get("machine_id") == self.mid)
        self.assertEqual(frame["upsert"], [reply["session"]])
        self.assertEqual(reply["session"]["state"], "unread")
        after = self.sessions()
        self.assertEqual([k for k, s in after.items() if s["state"] != "idle"], [(self.mid, self.sid)])


if __name__ == "__main__":
    unittest.main()
