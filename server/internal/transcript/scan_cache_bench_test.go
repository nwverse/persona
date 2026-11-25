package transcript

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bigClaudeTranscript writes a Claude transcript of about mb megabytes: questions, long answers, and now and then
// a background task with its notification, so the scan finds seed markers as a real one does.
func bigClaudeTranscript(tb testing.TB, mb int) string {
	tb.Helper()
	p := filepath.Join(tb.TempDir(), "big.jsonl")
	fh, err := os.Create(p)
	if err != nil {
		tb.Fatal(err)
	}
	w := bufio.NewWriterSize(fh, 1<<20)
	filler := strings.Repeat("lorem ipsum dolor sit amet ", 36)
	var written int64
	parent := ""
	for i := 0; written < int64(mb)<<20; i++ {
		var line string
		id := fmt.Sprintf("u%d", i)
		switch {
		case i%40 == 0:
			line = fmt.Sprintf(`{"type":"user","uuid":%q,"parentUuid":%q,"timestamp":"2026-11-10T10:00:00Z","message":{"role":"user","content":"question %d"}}`, id, parent, i)
		case i%997 == 0:
			line = fmt.Sprintf(`{"type":"user","uuid":%q,"parentUuid":%q,"timestamp":"2026-11-10T10:00:00Z","message":{"role":"user","content":"<task-notification>\n<task-id>t%d</task-id>\n<status>completed</status>\n</task-notification>"}}`, id, parent, i)
		case i%991 == 0:
			line = fmt.Sprintf(`{"type":"assistant","uuid":%q,"parentUuid":%q,"timestamp":"2026-11-10T10:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call%d","name":"Agent","input":{"description":"look","prompt":"go"}}]}}`, id, parent, i)
		default:
			line = fmt.Sprintf(`{"type":"assistant","uuid":%q,"parentUuid":%q,"timestamp":"2026-11-10T10:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"answer %d %s"}]}}`, id, parent, i, filler)
		}
		n, _ := w.WriteString(line + "\n")
		written += int64(n)
		parent = id
	}
	if err := w.Flush(); err != nil {
		tb.Fatal(err)
	}
	if err := fh.Close(); err != nil {
		tb.Fatal(err)
	}
	return p
}

func appendLine(tb testing.TB, p string, i int) {
	tb.Helper()
	fh, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		tb.Fatal(err)
	}
	defer fh.Close()
	if _, err := fmt.Fprintf(fh, `{"type":"assistant","uuid":"x%d","timestamp":"2026-11-10T10:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"more %d"}]}}`+"\n", i, i); err != nil {
		tb.Fatal(err)
	}
}

// coldPage is what a page read costs the node when it holds no parsed File for the session: a File opened on the
// path, read, and its latest exchanges paged.
func coldPage(tb testing.TB, p string) {
	f := NewFile(p)
	if _, _, err := f.RefreshSpan(); err != nil {
		tb.Fatal(err)
	}
	if items, _, _ := f.PageTurns(0, 2, 200); len(items) == 0 {
		tb.Fatal("no items")
	}
}

// BenchmarkColdPageBigTranscript: page reads of a 96 MB transcript, each from a File opened cold, with the file at
// rest and with a line appended before each read.
func BenchmarkColdPageBigTranscript(b *testing.B) {
	p := bigClaudeTranscript(b, 96)
	b.Run("unchanged", func(b *testing.B) {
		coldPage(b, p)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			coldPage(b, p)
		}
	})
	b.Run("appended", func(b *testing.B) {
		coldPage(b, p)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			appendLine(b, p, i)
			b.StartTimer()
			coldPage(b, p)
		}
	})
}
