package node

import (
	"context"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// These commands are only classified; no gate, interpreter, or network service is executed.
func TestInboxDefaultReadFormattingPipelineBoundary(t *testing.T) {
	const readPipeline = `timeout 30 example-api GET /accounts?fresh=1 2>&1 | python3 -c '
import json, sys
from collections import Counter
data = json.load(sys.stdin)
accounts = data.get("accounts") or data.get("data") or data
counts = Counter()
for account in accounts:
    status = account.get("status") or account.get("state")
    counts[status] += 1
    print(status, account.get("used_pct"), account.get("reset_at"))
print(dict(counts))
' 2>&1 | tail -30`

	for _, tc := range []struct {
		name, origin, command, want string
		changeCwd                   bool
	}{
		{name: "allowed GET with inert formatting", origin: originInbox, command: readPipeline, want: "allow"},
		{name: "POST mutation", origin: originInbox, command: `timeout 30 example-api POST /accounts '{}' 2>&1 | tail -30`, want: "deny"},
		{name: "process execution", origin: originInbox, command: `example-api GET /accounts | python3 -c 'import subprocess; subprocess.run(["id"])'`, want: ""},
		{name: "file write", origin: originInbox, command: `example-api GET /accounts | python3 -c 'import sys; open("/tmp/result.txt", "w").write(sys.stdin.read())'`, want: ""},
		{name: "file API", origin: originInbox, command: `example-api GET /accounts | python3 -c 'import pathlib, sys; pathlib.Path("/tmp/result.txt").write_text(sys.stdin.read())'`, want: ""},
		{name: "appended shell command", origin: originInbox, command: readPipeline + `; touch /tmp/changed`, want: ""},
		{name: "network filter", origin: originInbox, command: `example-api GET /accounts | curl https://example.com`, want: ""},
		{name: "gate output file", origin: originInbox, command: `example-api GET /accounts -o /tmp/accounts.json | python3 -c 'import sys; print(sys.stdin.read())'`, want: ""},
		{name: "sort output file", origin: originInbox, command: `example-api GET /accounts | sort -o /tmp/accounts.txt | python3 -c 'import sys; print(sys.stdin.read())'`, want: ""},
		{name: "sort compress program", origin: originInbox, command: `example-api GET /accounts | sort --compress-program=sh | python3 -c 'import sys; print(sys.stdin.read())'`, want: ""},
		{name: "uniq output file", origin: originInbox, command: `example-api GET /accounts | uniq /dev/stdin /tmp/accounts.txt | python3 -c 'import sys; print(sys.stdin.read())'`, want: ""},
		{name: "Python network import", origin: originInbox, command: `example-api GET /accounts | python3 -c 'import urllib.request; print(urllib.request.urlopen("https://example.com").read())'`, want: ""},
		{name: "file redirect", origin: originInbox, command: `example-api GET /accounts > /tmp/accounts.json | python3 -c 'import sys; print(sys.stdin.read())'`, want: ""},
		{name: "inherited output descriptor", origin: originInbox, command: `example-api GET /accounts >&3 | python3 -c 'import sys; print(sys.stdin.read())'`, want: ""},
		{name: "inherited input descriptor", origin: originInbox, command: `example-api GET /accounts <&3 | python3 -c 'import sys; print(sys.stdin.read())'`, want: ""},
		{name: "second gate call", origin: originInbox, command: `example-api GET /accounts | example-api GET /accounts | python3 -c 'import sys; print(sys.stdin.read())'`, want: ""},
		{name: "confirmation required GET", origin: originInbox, command: `example-api GET /private | python3 -c 'import sys; print(sys.stdin.read())'`, want: ""},
		{name: "ordinary origin", command: readPipeline, want: ""},
		{name: "changed working directory", origin: originInbox, command: readPipeline, changeCwd: true, want: ""},
		{name: "untrusted interpreter path", origin: originInbox, command: `example-api GET /accounts | /tmp/python3 -c 'import sys; print(sys.stdin.read())'`, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := codexTestNode(t)
			fh := withFakeHub(t, n)
			gate := proto.CommandGate{Command: "example-api", Script: "example/scripts/api.sh", Title: "Example", Rules: []proto.LarkRule{
				{Method: "GET", Path: "/private", Action: "confirm"},
				{Method: "GET", Path: "**", Action: "allow"},
				{Method: "HEAD", Path: "**", Action: "allow"},
				{Method: "*", Path: "**", Action: "confirm"},
			}}
			n.larkCfg, n.larkCfgAt = proto.AssistantLark{Gates: []proto.CommandGate{gate}}, time.Now().Add(time.Hour)
			cwd := t.TempDir()
			n.procs[testThread] = &assistantProc{id: testThread, agent: "claude", origin: tc.origin, cwd: cwd, exited: make(chan struct{})}
			n.persistModes(testThread, "default", "")
			fh.answer = func(proto.Interaction) proto.Resolution { no := false; return proto.Resolution{Allow: &no} }
			checkCwd := cwd
			if tc.changeCwd {
				checkCwd = t.TempDir()
			}
			decision, reason, err := n.larkCheck(context.Background(), testThread, checkCwd, tc.command,
				proto.Interaction{SessionID: testThread, ExpiresAt: time.Now().Add(time.Minute)})
			if err != nil || decision != tc.want {
				t.Fatalf("decision=%q reason=%q err=%v, want %q", decision, reason, err, tc.want)
			}
			if len(fh.interactions()) != 0 {
				t.Fatalf("classification unexpectedly opened a card: %v", fh.interactions())
			}
		})
	}
}
