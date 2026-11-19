package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestContextRestartRecommendation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	n := &Node{metas: map[string]quickMeta{}, bgScans: map[string]*bgScan{}}
	add := func(rows ...string) {
		t.Helper()
		for _, row := range rows {
			if _, err := fmt.Fprintln(file, row); err != nil {
				t.Fatal(err)
			}
		}
	}
	check := func(want bool) {
		t.Helper()
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		var session proto.Session
		setContext(&session, n.metaNow(path, st))
		wire, err := json.Marshal(&session)
		if err != nil {
			t.Fatal(err)
		}
		if got := bytes.Contains(wire, []byte(`"context_restart_recommended":true`)); got != want {
			t.Fatalf("context restart on wire = %v, want %v", got, want)
		}
		key := listKey([]proto.Session{session})
		session.ContextRestartRecommended = !session.ContextRestartRecommended
		if key == listKey([]proto.Session{session}) {
			t.Fatal("context restart change did not change the published list key")
		}
	}
	high := func(id, text string) string {
		return fmt.Sprintf(`{"type":"assistant","message":{"id":%q,"content":[{"type":"text","text":%q}],"usage":{"input_tokens":10,"cache_read_input_tokens":260000,"cache_creation_input_tokens":50000}}}`, id, text)
	}
	for _, tc := range []struct {
		name string
		run  func()
	}{
		{"two distinct requests despite repeated usage and a long reply", func() {
			add(high("a", ""), high("a", ""), high("b", strings.Repeat("x", quickTail+1)), high("a", ""))
			add(`{"type":"assistant","isSidechain":true,"message":{"id":"side","usage":{"input_tokens":400000}}}`,
				`{"type":"assistant","isApiErrorMessage":true,"message":{"id":"error","usage":{"input_tokens":400000}}}`,
				`{"type":"assistant","message":{"id":"aggregate","usage":{"input_tokens":1700000}}}`,
				`{"type":"api-request-blob","data":{"usage":{"input_tokens":1000000,"output_tokens":0}}}`)
			check(false)
		}},
		{"third large request", func() {
			add(high("c", ""))
			check(true)
		}},
		{"compact clears the recommendation and the partial streak", func() {
			add(`{"type":"system","subtype":"compact_boundary","compactMetadata":{"preTokens":510000,"postTokens":9000}}`)
			check(false)
			add(high("d", ""), high("e", ""))
			check(false)
			add(high("f", ""))
			check(true)
		}},
		{"a lower request breaks the streak without a compact boundary", func() {
			add(`{"type":"assistant","message":{"id":"g","usage":{"input_tokens":300000}}}`)
			check(false)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) { tc.run() })
	}
}

func TestContextRestartRecommendationCodex(t *testing.T) {
	var transcript strings.Builder
	add := func(total int) {
		fmt.Fprintf(&transcript, `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":%d},"last_token_usage":{"input_tokens":310000,"cached_input_tokens":300000}}}}`+"\n", total)
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	check := func(want bool) {
		t.Helper()
		if err := os.WriteFile(path, []byte(transcript.String()), 0600); err != nil {
			t.Fatal(err)
		}
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		n := &Node{metas: map[string]quickMeta{}, bgScans: map[string]*bgScan{}}
		m := n.meta(path, st)
		if m.contextRestartRecommended != want {
			t.Fatalf("Codex context restart: got %v, want %v", m.contextRestartRecommended, want)
		}
	}
	add(310001)
	add(310001)
	add(620002)
	check(false)
	add(930003)
	check(true)
	transcript.WriteString(`{"type":"compacted","payload":{"message":""}}` + "\n")
	add(930003)
	add(1240004)
	add(1550005)
	check(false)
	add(1860006)
	check(true)
}
