package node

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func agyPlannerLine(idx int, status string, ts time.Time, model string, in, cacheRead, out int) string {
	return fmt.Sprintf(
		`{"step_index":%d,"source":"MODEL","type":"PLANNER_RESPONSE","status":%q,"created_at":%q,"model":%q,"input_tokens":%d,"cache_read_tokens":%d,"output_tokens":%d}`+"\n",
		idx, status, ts.UTC().Format(time.RFC3339), model, in, cacheRead, out,
	)
}

func writeAgyTranscript(t *testing.T, root, conv, rel, body string) string {
	t.Helper()
	p := filepath.Join(root, "brain", conv, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Antigravity (agy) transcripts under ~/.gemini/antigravity-cli/brain/<id> are metered as agent "agy"
// with input_tokens exclusive of cache_read_tokens, supporting transcript_full.jsonl fallback,
// incremental append, truncation/rewind dedup, cross-file fork dedup, parallel same-second sessions,
// launcher lookup, and startDir attribution.
func TestUsageMeterAgyEndToEnd(t *testing.T) {
	t.Setenv("ANTIGRAVITY_CLI_CONFIG_DIR", "")
	n, home := testNode(t)
	now := time.Now().UTC().Truncate(time.Minute).Add(20 * time.Second)
	root := filepath.Join(home, ".gemini", "antigravity-cli")

	const conv1 = "11111111-1111-4111-8111-111111111111"
	const conv2 = "22222222-2222-4222-8222-222222222222"
	const convFork = "33333333-3333-4333-8333-333333333333"
	const convParallel = "44444444-4444-4444-8444-444444444444"

	// conv1 has both transcript.jsonl and transcript_full.jsonl: only transcript.jsonl should be listed.
	step1 := agyPlannerLine(1, "DONE", now, "gemini-4-neon-medium", 23514, 0, 795)
	step2 := agyPlannerLine(7, "DONE", now.Add(5*time.Second), "gemini-4-neon-medium", 7736, 23515, 789)
	p1 := writeAgyTranscript(t, root, conv1, agyTranscriptPath,
		`{"step_index":0,"type":"USER_INPUT","status":"DONE","created_at":"`+now.Format(time.RFC3339)+`","content":"hello"}`+"\n"+
			agyPlannerLine(1, "RUNNING", now, "gemini-4-neon-medium", 100, 0, 10)+
			step1+
			"not-json\n"+
			step2)
	writeAgyTranscript(t, root, conv1, agyFullTranscriptPath, step1+step2)
	if err := os.WriteFile(filepath.Join(root, "brain", conv1, ".persona-session.json"),
		[]byte(`{"cwd":"/work/app","launcher":"agy"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// conv2 has only transcript_full.jsonl.
	writeAgyTranscript(t, root, conv2, agyFullTranscriptPath,
		agyPlannerLine(1, "DONE", now, "gemini-4-neon-medium", 1000, 4000, 200))

	// convFork copies step1 verbatim (must be deduplicated) and adds its own step 9.
	writeAgyTranscript(t, root, convFork, agyTranscriptPath,
		step1+agyPlannerLine(9, "DONE", now, "gemini-4-neon-medium", 500, 1500, 100))

	// convParallel runs at the exact same second & step_index=1 as conv1, with different token counts (must both count).
	writeAgyTranscript(t, root, convParallel, agyTranscriptPath,
		agyPlannerLine(1, "DONE", now, "gemini-4-neon-medium", 1234, 0, 56))

	// Hidden directory must be skipped.
	writeAgyTranscript(t, root, ".hidden", agyTranscriptPath,
		agyPlannerLine(1, "DONE", now, "gemini-4-neon-medium", 9999, 0, 999))

	m := newUsageMeter()
	sessions := map[string]bool{}
	m.launcherOf = func(s string) string { sessions[s] = true; return "agy-launcher" }
	cutoff := now.Add(-time.Hour)

	var listed []usagePath
	for _, f := range n.usageFiles(cutoff) {
		if f.ad.agent() == agyAgent {
			listed = append(listed, f)
			m.scan(f, cutoff)
		}
	}
	if len(listed) != 4 {
		t.Fatalf("listed %d agy files, want 4 (skipping duplicate full transcript and .hidden)", len(listed))
	}
	if got := (agyUsage{}).startDir(p1); got != "/work/app" {
		t.Fatalf("startDir(p1) = %q, want /work/app", got)
	}
	for _, id := range []string{conv1, conv2, convFork, convParallel} {
		if !sessions[agyAgent+"-"+id] {
			t.Errorf("launcherOf not asked for %s (asked %v)", agyAgent+"-"+id, sessions)
		}
	}

	minute := now.Unix() / 60 * 60
	b := m.buckets[usageKey{minute, "gemini-4-neon-medium", "agy-launcher", agyAgent, ""}]
	if b == nil {
		t.Fatalf("missing agy bucket; buckets=%+v", m.buckets)
	}
	// Requests: conv1 (2) + conv2 (1) + convFork (1 new; step1 deduped) + convParallel (1) = 5.
	wantIn := int64(23514 + 7736 + 1000 + 500 + 1234)
	wantCacheRead := int64(0 + 23515 + 4000 + 1500 + 0)
	wantOut := int64(795 + 789 + 200 + 100 + 56)
	if b.Requests != 5 || b.Input != wantIn || b.CacheRead != wantCacheRead || b.Output != wantOut {
		t.Fatalf("bucket = %+v, want req=5 in=%d cache_read=%d out=%d", b, wantIn, wantCacheRead, wantOut)
	}

	// Incremental append: only the newly appended step is counted.
	step3 := agyPlannerLine(12, "DONE", now.Add(10*time.Second), "gemini-4-neon-medium", 300, 900, 50)
	fh, err := os.OpenFile(p1, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fh.WriteString(step3); err != nil {
		fh.Close()
		t.Fatal(err)
	}
	fh.Close()
	st, err := os.Stat(p1)
	if err != nil {
		t.Fatal(err)
	}
	m.scan(usagePath{p1, st, agyUsage{}}, cutoff)
	if b.Requests != 6 || b.Input != wantIn+300 || b.CacheRead != wantCacheRead+900 || b.Output != wantOut+50 {
		t.Fatalf("after append: bucket = %+v", b)
	}

	// Truncation/rewind: rewrite p1 shorter with step1 (already counted) + a new replacement step.
	stepRewound := agyPlannerLine(7, "DONE", now.Add(15*time.Second), "gemini-4-neon-medium", 400, 2000, 70)
	if err := os.WriteFile(p1, []byte(step1+stepRewound), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err = os.Stat(p1)
	if err != nil {
		t.Fatal(err)
	}
	m.scan(usagePath{p1, st, agyUsage{}}, cutoff)
	// step1 must NOT be double-counted; stepRewound MUST be counted.
	if b.Requests != 7 || b.Input != wantIn+300+400 || b.CacheRead != wantCacheRead+900+2000 || b.Output != wantOut+50+70 {
		t.Fatalf("after rewind: bucket = %+v", b)
	}
}

// Custom ANTIGRAVITY_CLI_CONFIG_DIR, extra multi-account roots (with symlink dedup), empty/missing dir,
// session-scope exclusion, and firstTime("created_at") are handled cleanly.
func TestUsageAgyRootsScopeAndFirstTime(t *testing.T) {
	n, _ := testNode(t)
	custom := filepath.Join(t.TempDir(), "custom-agy")
	extra := filepath.Join(t.TempDir(), "extra-agy")
	extraLink := filepath.Join(t.TempDir(), "extra-link")
	if err := os.MkdirAll(extra, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(extra, extraLink); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTIGRAVITY_CLI_CONFIG_DIR", custom)
	n.agyRoots.mu.Lock()
	n.agyRoots.extra = []string{extra, extraLink}
	n.agyRoots.mu.Unlock()

	now := time.Now().UTC().Truncate(time.Second)
	// Empty/missing brain dir returns nothing without error.
	if got := (agyUsage{}).paths(n, now.Add(-time.Hour)); len(got) != 0 {
		t.Fatalf("empty dirs paths = %v, want empty", got)
	}

	writeAgyTranscript(t, custom, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", agyTranscriptPath,
		agyPlannerLine(1, "DONE", now, "gemini-4-neon-medium", 10, 0, 2))
	writeAgyTranscript(t, extra, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", agyTranscriptPath,
		agyPlannerLine(1, "DONE", now, "gemini-4-neon-medium", 20, 0, 3))

	got := (agyUsage{}).paths(n, now.Add(-time.Hour))
	if len(got) != 2 {
		t.Fatalf("paths = %v, want 2 (symlinked extra root deduped)", got)
	}

	// Session-scoped node must not list global agy transcripts.
	scoped := scopedTestNode(t)
	if got := (agyUsage{}).paths(scoped, now.Add(-time.Hour)); len(got) != 0 {
		t.Fatalf("scoped paths = %v, want empty", got)
	}

	// firstTime recognizes agy's created_at field for large-file startOffset seeking.
	ts := firstTime([]byte(`{"step_index":1,"type":"PLANNER_RESPONSE","created_at":"2026-11-28T08:54:18Z","input_tokens":1}`))
	if !ts.Equal(time.Date(2026, 11, 28, 8, 54, 18, 0, time.UTC)) {
		t.Fatalf("firstTime(created_at) = %v", ts)
	}
}
