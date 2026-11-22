package node

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

const cacheFixtureBody = `{"model":"example-model","system":[{"type":"text","text":"fixture prompt","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","content":"fixture"}],"max_tokens":100,"stream":true}`

// The isolated fake CLI uses the last --settings env over its launcher's process
// env, as Claude does. It calls a fake provider, then emits that provider message
// id on the root stream. It never invokes a real CLI or reads real configuration.
const cacheWirePython = `import json, os, sys, urllib.request
args=sys.argv[1:]
base=os.environ.get('ANTHROPIC_BASE_URL','')
for i, arg in enumerate(args):
 if arg=='--settings' and i+1<len(args):
  try:
   with open(args[i+1]) as f: base=json.load(f).get('env',{}).get('ANTHROPIC_BASE_URL',base)
  except FileNotFoundError: pass
body=os.environ['CACHE_FIXTURE_BODY'].encode()
req=urllib.request.Request(base.rstrip('/')+'/v1/messages',body,{'Content-Type':'application/json','X-Api-Key':'fixture-key'})
with urllib.request.urlopen(req,timeout=5) as r: raw=r.read().decode()
for line in raw.splitlines():
 if line.startswith('data: '):
  e=json.loads(line[6:])
  if e.get('type')=='message_start':
   print(json.dumps({'type':'stream_event','parent_tool_use_id':None,'event':e}),flush=True)
`

func TestAssistantCacheProxyScopeAndLauncherPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		allow, managed, override bool
	}{{"main", true, false, false}, {"lead", true, true, false}, {"background", false, false, false}, {"worker", false, false, false}, {"child", false, false, false}, {"override", true, false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			f := cliTest(t)
			t.Setenv("CACHE_FIXTURE_BODY", cacheFixtureBody)
			writeFile(t, filepath.Join(f.dir, "cache-wire.py"), cacheWirePython)
			script := strings.Replace(fakeCLI, `*'"type":"user"'*)`, `*'"type":"user"'*)`+"\n      python3 \"$CLI_FAKE_DIR/cache-wire.py\" \"$@\"", 1)
			writeScript(t, filepath.Join(os.Getenv("HOME"), "bin", "claude"), "#!/bin/sh\n"+script, 0o700)
			var requests atomic.Int32
			var marked atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				requests.Add(1)
				if r.Header.Get("X-Persona-Request-Kind") == "cache-keepalive" {
					marked.Add(1)
					_, _ = io.WriteString(w, `{"usage":{"cache_read_input_tokens":1234,"cache_creation_input_tokens":0,"output_tokens":1}}`)
					return
				}
				if string(b) != cacheFixtureBody {
					t.Error("normal provider bytes changed")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_fixture\",\"model\":\"example-model\",\"usage\":{\"input_tokens\":1}}}\n\n")
			}))
			defer upstream.Close()
			launcher := "gw"
			if tc.managed {
				f.fakeLaunch(t)
				f.n.setManaged([]proto.ManagedLauncher{{Name: launcher, Kind: "api", BaseURL: upstream.URL, Model: "example-model"}})
			} else {
				rc := fmt.Sprintf("function gw { export ANTHROPIC_BASE_URL=%q; command claude \"$@\"; }\n", upstream.URL)
				if tc.override {
					file := filepath.Join(os.Getenv("HOME"), "override.json")
					writeFile(t, file, fmt.Sprintf(`{"env":{"ANTHROPIC_BASE_URL":%q}}`, upstream.URL))
					rc = fmt.Sprintf("function gw { command claude \"$@\" --settings %q; }\n", file)
				}
				writeFile(t, filepath.Join(os.Getenv("HOME"), ".zshrc"), rc)
			}
			e := cliExec()
			e.Model = "example-model"
			e.IsMain = tc.name == "main"
			e.FollowsSetting = tc.name == "lead" || tc.name == "background"
			policy := &assistantwire.CacheKeepalive{Enabled: true, IntervalMinutes: 55, MaxRequests: 24}
			if tc.allow {
				e.CacheKeepalive = policy
			}
			p, err := f.n.spawnClaude(newUUID(), t.TempDir(), launcher, false, false, e)
			if err != nil {
				t.Fatal(err)
			}
			defer f.n.stopAssistant(p.id)
			if (p.cacheProxy != nil) != tc.allow {
				t.Fatal("wrong process got a proxy")
			}
			if err := p.write([]byte(`{"type":"user","message":{"role":"user","content":"fixture"}}`)); err != nil {
				t.Fatal(err)
			}
			cliWait(t, "fake provider request", func() bool { return requests.Load() == 1 })
			f.idle(t, p.id)
			before := f.file("stdin.jsonl")
			if p.cacheProxy != nil {
				p.cacheProxy.Tick(context.Background(), time.Now().Add(55*time.Minute), policy, true)
			}
			want := int32(0)
			if tc.allow && !tc.override {
				want = 1
			}
			if marked.Load() != want {
				t.Fatalf("refreshes %d want %d", marked.Load(), want)
			}
			if f.file("stdin.jsonl") != before {
				t.Fatal("keepalive wrote a conversation input")
			}
			if strings.Contains(f.log.String(), "fixture-key") || strings.Contains(f.log.String(), "fixture prompt") {
				t.Fatal("private log")
			}
		})
	}
}
func TestAssistantCacheProxyExcludesUnsupportedAndSpare(t *testing.T) {
	f := cliTest(t)
	c := assistantCompaction{endpoint: "https://api.example.com", endpointKnown: true}
	on := &assistantwire.CacheKeepalive{Enabled: true, IntervalMinutes: 55, MaxRequests: 24}
	for _, e := range []modelExecution{{IsMain: true}, {FollowsSetting: true}, {CacheKeepalive: on, Strict: true}} {
		if p := f.n.startCacheWarm("example", "claude", c, e, false); p != nil {
			p.Close()
			t.Fatal("excluded execution mounted proxy")
		}
	}
	if p := f.n.startCacheWarm("example", "claude", c, modelExecution{CacheKeepalive: on}, true); p != nil {
		p.Close()
		t.Fatal("spare/guest mounted proxy")
	}
	if p := f.n.startCacheWarm("example", "external:example", c, modelExecution{CacheKeepalive: on}, false); p != nil {
		p.Close()
		t.Fatal("external runtime mounted proxy")
	}
}
