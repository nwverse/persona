package kb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The search at the size history imports bring (#1273): the archive of find_bench_test.go's generator at 2,000 chats
// over ten years, and the first search of a new engine (cold), a repeated one (warm) and kb_search, each in a process
// of its own so its peak memory is its own. Off unless asked for:
//
//	PERSONA_KB_SCALE_LINES=4000000 go test ./internal/kb -run '^TestSearchScale$' -v -timeout 0
//
// PERSONA_KB_SCALE_DIR keeps the archive there between runs (it is built when the directory does not hold one of that
// size; without it a temporary directory is used and removed), PERSONA_KB_SCALE_RUNS is how many times each search is
// timed (5; kb_search 3), PERSONA_KB_SCALE_PROCS the GOMAXPROCS values to run with ("0,2": unset and 2), and
// PERSONA_KB_SCALE_TEXT=varied gives every message a text of its own (variedText) instead of the one repeated line.
const (
	scaleChats    = 2000
	scaleSpanDays = 3650
	scaleLines    = 11
	scaleMark     = "SCALE-RESULT "
)

// scaleShape is the archive of about lines lines: day files of scaleLines lines spread evenly over ten years.
func scaleShape(lines int) archiveShape {
	days := max(1, lines/(scaleChats*scaleLines))
	return archiveShape{chats: scaleChats, days: days, stride: max(1, scaleSpanDays/days), lines: scaleLines,
		start: time.Date(2016, 11, 3, 0, 0, 0, 0, time.UTC), varied: os.Getenv("PERSONA_KB_SCALE_TEXT") == "varied"}
}

type scaleTiming struct {
	Name   string  `json:"name"`
	Median float64 `json:"median_s"`
	Max    float64 `json:"max_s"`
	Runs   int     `json:"runs"`
	Found  int     `json:"found"` // results (Find) or hits (Search) of the last run
}

type scaleReport struct {
	Case    string        `json:"case"`
	Procs   int           `json:"procs"`
	CPUs    int           `json:"cpus"`
	Timings []scaleTiming `json:"timings"`
	PeakRSS int64         `json:"peak_rss"` // bytes; 0 where it cannot be read
}

func (r scaleReport) timing(name string) scaleTiming {
	for _, t := range r.Timings {
		if t.Name == name {
			return t
		}
	}
	return scaleTiming{}
}

// peakRSS is the most memory the process has held (VmHWM), 0 where /proc does not say.
func peakRSS() int64 {
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(raw), "\n") {
		if rest, ok := strings.CutPrefix(l, "VmHWM:"); ok {
			f := strings.Fields(rest)
			if len(f) >= 1 {
				kb, _ := strconv.ParseInt(f[0], 10, 64)
				return kb << 10
			}
		}
	}
	return 0
}

func scaleEnvInt(name string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(name)); err == nil && n > 0 {
		return n
	}
	return def
}

// timeRuns times f runs times and returns the median and the slowest.
func timeRuns(name string, runs int, f func() int) scaleTiming {
	took := make([]float64, 0, runs)
	found := 0
	for range runs {
		start := time.Now()
		found = f()
		took = append(took, time.Since(start).Seconds())
	}
	slices.Sort(took)
	return scaleTiming{Name: name, Median: took[len(took)/2], Max: took[len(took)-1], Runs: runs, Found: found}
}

// TestSearchScaleChild is one measurement of TestSearchScale, in its own process (PERSONA_KB_SCALE_CHILD names it).
func TestSearchScaleChild(t *testing.T) {
	which := os.Getenv("PERSONA_KB_SCALE_CHILD")
	if which == "" {
		t.Skip("run by TestSearchScale")
	}
	// cold is an engine as a process just started has it: nothing kept from an earlier search.
	cold := func() *Engine {
		st, err := NewStore(os.Getenv("PERSONA_KB_SCALE_DATA"))
		if err != nil {
			t.Fatal(err)
		}
		return &Engine{Store: st}
	}
	runs := scaleEnvInt("PERSONA_KB_SCALE_RUNS", 5)
	rep := scaleReport{Case: which, Procs: runtime.GOMAXPROCS(0), CPUs: runtime.NumCPU()}
	// A search before the timed ones, so the files are in the OS page cache for every run, the first too.
	cold().Find("周报", 80)
	switch which {
	case "find":
		for _, q := range []struct{ name, query string }{{"rare", "周报"}, {"every", "build"}, {"none", "nowhere"}} {
			rep.Timings = append(rep.Timings, timeRuns("cold "+q.name, runs, func() int { return len(cold().Find(q.query, 80)) }))
			warm := cold()
			warm.Find(q.query, 80)
			rep.Timings = append(rep.Timings, timeRuns("warm "+q.name, runs, func() int { return len(warm.Find(q.query, 80)) }))
		}
	case "search":
		runs = scaleEnvInt("PERSONA_KB_SCALE_RUNS", 3)
		for _, q := range []struct{ name, query string }{{"rare", "周报 draft"}, {"absent", "nowhere"}} {
			rep.Timings = append(rep.Timings, timeRuns("cold "+q.name, runs, func() int { return len(cold().Search(q.query, true, 40)) }))
		}
	default:
		t.Fatalf("unknown case %q", which)
	}
	rep.PeakRSS = peakRSS()
	out, _ := json.Marshal(rep)
	fmt.Println(scaleMark + string(out))
}

// scaleArchive is the data directory of the archive of that shape under root, built unless root already holds it.
func scaleArchive(t *testing.T, root string, shape archiveShape) string {
	t.Helper()
	data := filepath.Join(root, "data")
	mark := filepath.Join(root, "shape")
	want := fmt.Sprintf("%d chats, %d days every %d, %d lines, varied %v\n", shape.chats, shape.days, shape.stride, shape.lines, shape.varied)
	if have, err := os.ReadFile(mark); err == nil && string(have) == want {
		return data
	}
	if err := os.RemoveAll(data); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := fillArchive(data, shape); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mark, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("built the archive in %s", time.Since(start).Round(time.Second))
	return data
}

// scaleChild runs one measurement in a process of its own, with GOMAXPROCS=procs when procs > 0.
func scaleChild(t *testing.T, data, which string, procs int, env ...string) scaleReport {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(scaleEnvInt("PERSONA_KB_SCALE_TIMEOUT_MIN", 90))*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSearchScaleChild$", "-test.timeout=0")
	cmd.Env = append(os.Environ(), "PERSONA_KB_SCALE_CHILD="+which, "PERSONA_KB_SCALE_DATA="+data)
	cmd.Env = append(cmd.Env, env...)
	if procs > 0 {
		cmd.Env = append(cmd.Env, "GOMAXPROCS="+strconv.Itoa(procs))
	}
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("%s (GOMAXPROCS=%d): timed out\n%s", which, procs, out)
	}
	if err != nil {
		t.Fatalf("%s (GOMAXPROCS=%d): %v\n%s", which, procs, err, out)
	}
	for _, l := range strings.Split(string(out), "\n") {
		if raw, ok := strings.CutPrefix(l, scaleMark); ok {
			var rep scaleReport
			if err := json.Unmarshal([]byte(raw), &rep); err != nil {
				t.Fatal(err)
			}
			return rep
		}
	}
	t.Fatalf("%s (GOMAXPROCS=%d): no result\n%s", which, procs, out)
	return scaleReport{}
}

func scaleSeconds(t scaleTiming) string {
	if t.Runs == 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f s / %.2f s", t.Median, t.Max)
}

func scaleMB(n int64) string {
	if n <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%d MB", n>>20)
}

func scaleProcs(procs int) string {
	if procs <= 0 {
		return "unset"
	}
	return strconv.Itoa(procs)
}

// TestSearchScale prints the issue's table for this machine: Find and kb_search over the archive of
// PERSONA_KB_SCALE_LINES lines, cold and warm, with GOMAXPROCS unset and 2, and each process's peak memory.
func TestSearchScale(t *testing.T) {
	lines := scaleEnvInt("PERSONA_KB_SCALE_LINES", 0)
	if lines == 0 {
		t.Skip("PERSONA_KB_SCALE_LINES not set")
	}
	root := os.Getenv("PERSONA_KB_SCALE_DIR")
	if root == "" {
		tmp, err := os.MkdirTemp("", "kb-scale-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tmp)
		root = tmp
	} else if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	shape := scaleShape(lines)
	data := scaleArchive(t, root, shape)
	var procs []int
	for _, f := range strings.Split(cmpOr(os.Getenv("PERSONA_KB_SCALE_PROCS"), "0,2"), ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			t.Fatalf("PERSONA_KB_SCALE_PROCS: %v", err)
		}
		procs = append(procs, n)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d lines in %d day files (%d chats), %d CPUs; median / max\n\n", shape.chats*shape.days*shape.lines,
		shape.chats*shape.days, shape.chats, runtime.NumCPU())
	sb.WriteString("| GOMAXPROCS | Cold Find rare | Warm Find rare | Cold Find every | Warm Find every | Cold Find none | Warm Find none | Peak RSS |\n")
	sb.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- |\n")
	var search strings.Builder
	search.WriteString("| GOMAXPROCS | Cold kb_search rare | Cold kb_search absent | Peak RSS |\n| --- | --- | --- | --- |\n")
	for _, p := range procs {
		find := scaleChild(t, data, "find", p)
		fmt.Fprintf(&sb, "| %s | %s | %s | %s | %s | %s | %s | %s |\n", scaleProcs(p), scaleSeconds(find.timing("cold rare")),
			scaleSeconds(find.timing("warm rare")), scaleSeconds(find.timing("cold every")), scaleSeconds(find.timing("warm every")),
			scaleSeconds(find.timing("cold none")), scaleSeconds(find.timing("warm none")), scaleMB(find.PeakRSS))
		s := scaleChild(t, data, "search", p)
		fmt.Fprintf(&search, "| %s | %s | %s | %s |\n", scaleProcs(p), scaleSeconds(s.timing("cold rare")),
			scaleSeconds(s.timing("cold absent")), scaleMB(s.PeakRSS))
	}
	fmt.Printf("\n%s\n%s\nsearch scale: ok\n", sb.String(), search.String())
}

func cmpOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
