package errreport

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// Build is what the hub and the manager say of themselves in a report.
type Build struct {
	Source  string // wire.ErrorSourceHub or wire.ErrorSourceManager
	Release string
	Build   string
	Commit  string
}

// NewID is a fresh report id.
func NewID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (b Build) report(kind, level, message string) wire.ErrorReport {
	return wire.ErrorReport{ID: NewID(), Source: b.Source, Kind: kind, Level: level, Time: time.Now().UTC().Format(time.RFC3339Nano),
		Release: b.Release, Build: b.Build, Commit: b.Commit, OS: runtime.GOOS, OSVersion: runtime.GOARCH, Message: message}
}

// Panic is the report of a recovered panic: its value and the stack (debug.Stack()) as frames; where says what was
// running ("GET /api/v1/inbox", a route pattern, never a path with ids).
func (b Build) Panic(v any, stack []byte, where string) wire.ErrorReport {
	r := b.report(wire.ErrorKindPanic, wire.ErrorLevelFatal, fmt.Sprintf("panic: %v", v))
	typ := fmt.Sprintf("%T", v)
	if err, ok := v.(error); ok {
		typ = fmt.Sprintf("%T", err)
	}
	r.Exceptions = []wire.ErrorException{{Type: typ, Value: fmt.Sprint(v), Frames: GoFrames(stack)}}
	if where != "" {
		r.Tags = map[string]string{"where": where}
	}
	return r
}

// Line is the report of an error line of a log.
func (b Build) Line(line string) wire.ErrorReport {
	return b.report(wire.ErrorKindLog, wire.ErrorLevelError, strings.TrimSpace(logTime.ReplaceAllString(line, "")))
}

var (
	goFunc  = lazyre.New(`^(\S+)\(.*\)$`)
	goFile  = lazyre.New(`^\t(.+):(\d+)(?: \+0x[0-9a-f]+)?$`)
	ownCode = "github.com/nwverse/persona/"
)

// GoFrames reads a goroutine's stack as debug.Stack() writes it into frames, innermost last; the recovering frames
// (runtime/debug, the panic itself) are left out.
func GoFrames(stack []byte) []wire.ErrorFrame {
	lines := strings.Split(string(stack), "\n")
	var out []wire.ErrorFrame
	for i := 0; i+1 < len(lines); i++ {
		m := goFunc.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		f := goFile.FindStringSubmatch(lines[i+1])
		if f == nil {
			continue
		}
		i++
		fn := m[1]
		if strings.HasPrefix(fn, "runtime/debug.") || strings.HasPrefix(fn, "runtime.gopanic") || fn == "panic" {
			out = out[:0] // what ran before the panic is below it
			continue
		}
		module, function := splitGoFunc(fn)
		line, _ := strconv.Atoi(f[2])
		out = append(out, wire.ErrorFrame{Function: function, Module: module, File: goFilePath(f[1]), Line: line,
			InApp: strings.HasPrefix(fn, ownCode)})
	}
	// debug.Stack is outermost last; the schema wants innermost last
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if len(out) > FramesMost {
		out = out[len(out)-FramesMost:]
	}
	return out
}

// splitGoFunc splits "github.com/x/y/pkg.(*T).M" into its package and "(*T).M".
func splitGoFunc(fn string) (string, string) {
	slash := strings.LastIndexByte(fn, '/')
	dot := strings.IndexByte(fn[slash+1:], '.')
	if dot < 0 {
		return "", fn
	}
	return fn[:slash+1+dot], fn[slash+1+dot+1:]
}

// goFilePath keeps a source file's path from its module on (".../server/internal/hub/http.go"), never the build
// machine's directories.
func goFilePath(p string) string {
	for _, mark := range []string{"/server/internal/", "/sdk/go/", "/pkg/mod/", "/src/"} {
		if i := strings.LastIndex(p, mark); i >= 0 {
			return strings.TrimPrefix(p[i:], "/")
		}
	}
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Reported marks a log write whose error was reported already (a recovered panic): the Tee skips it.
const Reported = "[reported]"

// errorLine says whether a log line is an error worth a report.
var errorLine = lazyre.New(`(?i)\bpanic\b|\berror\b|\berr\b[:=]|\bfailed\b|\bfailure\b|\bfatal\b| -> 5\d\d \(`)

// IsErrorLine says whether a log line is an error.
func IsErrorLine(line string) bool { return errorLine.MatchString(line) }

// Tee is a log's writer that also turns its error lines into reports, while On says reports are taken (nothing is
// built otherwise). Lines go to the writer as they are, first.
type Tee struct {
	W     io.Writer
	Build Build
	On    func() bool
	Sink  func(wire.ErrorReport)

	mu   sync.Mutex
	part []byte
}

// Write passes p on, then reports its error lines.
func (t *Tee) Write(p []byte) (int, error) {
	n, err := t.W.Write(p)
	if t.On == nil || t.Sink == nil || bytes.Contains(p, []byte(Reported)) || !t.On() {
		t.mu.Lock()
		t.part = t.part[:0]
		t.mu.Unlock()
		return n, err
	}
	t.mu.Lock()
	t.part = append(t.part, p...)
	var lines []string
	for {
		i := bytes.IndexByte(t.part, '\n')
		if i < 0 {
			break
		}
		lines = append(lines, string(t.part[:i]))
		t.part = t.part[i+1:]
	}
	if len(t.part) > 64<<10 {
		t.part = t.part[:0]
	}
	t.mu.Unlock()
	for _, l := range lines {
		if IsErrorLine(l) {
			t.Sink(t.Build.Line(l))
		}
	}
	return n, err
}
