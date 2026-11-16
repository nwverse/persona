package node

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestRoutineCheck(t *testing.T) {
	if loginShellProgram() == "" {
		t.Skip("node shell unavailable")
	}
	for _, tc := range []struct {
		name, command string
		code          int
		reason        string
		timeout       bool
	}{
		{name: "pass", command: "exit 0"},
		{name: "skip", command: "printf 'holiday\\nmore\\n'; exit 1", code: 1, reason: "holiday"},
		{name: "error", command: "printf 'failed\\n' >&2; exit 4", code: 4, reason: "failed"},
		{name: "timeout", command: "sleep 30; exit 0", timeout: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(proto.RoutineCheckParams{Command: tc.command, Timeout: 1})
			if err != nil {
				t.Fatal(err)
			}
			result, err := routineCheck(t.Context(), raw)
			if tc.timeout {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("timeout = %v", err)
				}
				return
			}
			if err != nil || result.ExitCode != tc.code || result.Reason != tc.reason {
				t.Fatalf("result = %+v, error = %v", result, err)
			}
		})
	}
}

func TestRoutineCheckDeadlineAfterShellExit(t *testing.T) {
	if runtime.GOOS == "windows" || loginShellProgram() == "" {
		t.Skip("requires a Unix shell")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	raw, err := json.Marshal(proto.RoutineCheckParams{Command: "sleep 30 & exit 0", Timeout: 10})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := routineCheck(ctx, raw); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
		t.Fatalf("background child kept the check waiting for %s", elapsed)
	}
}

// With no terminal (a node running as a service) an interactive bash first says so on stderr, and it ends with "exit"
// there when the check calls the exit builtin; the reason is still the first line of the check's own output (#8876).
func TestRoutineCheckReasonSkipsTheShellsJobControlWarning(t *testing.T) {
	long := strings.Repeat("x", 600)
	for _, tc := range []struct {
		name   string
		writes []string
		want   string
	}{
		{"bash without a terminal", []string{"bash: cannot set terminal process group (-1): Inappropriate ioctl for device\nbash: no job control in this shell\nholiday\nmore\n"}, "holiday"},
		{"in pieces", []string{"bash: cannot set terminal pro", "cess group (-1): Inappropriate ioctl for device\nbash: no job ", "control in this shell\nhol", "iday\n"}, "holiday"},
		{"zsh", []string{"zsh: can't set tty pgrp: inappropriate ioctl for device\nfailed\n"}, "failed"},
		{"bash in zh_CN", []string{"bash: 无法设定终端进程组 (-1): 对设备不适当的 ioctl 操作\nbash: 此 shell 中无任务控制\nholiday\nexit\n"}, "holiday"},
		{"only the warning", []string{"bash: no job control in this shell\n"}, ""},
		{"nothing printed, then bash's exit", []string{"bash: cannot set terminal process group (-1): Inappropriate ioctl for device\nbash: no job control in this shell\nexit\n"}, ""},
		{"a login bash says logout", []string{"logout\n"}, ""},
		{"exit after the reason", []string{"holiday\nexit\n"}, "holiday"},
		{"exit within a line of its own", []string{"exit code 3\n"}, "exit code 3"},
		{"no newline at the end", []string{"bash: no job control in this shell\nholiday"}, "holiday"},
		{"a terminal: unchanged", []string{"holiday\nmore\n"}, "holiday"},
		{"a long line is cut at 512", []string{long + "\n"}, long[:512]},
		{"the words in a sentence of its own", []string{"no job control here, said the check\n"}, "no job control here, said the check"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var w routineCheckLine
			for _, s := range tc.writes {
				if n, err := w.Write([]byte(s)); n != len(s) || err != nil {
					t.Fatalf("Write = %d, %v", n, err)
				}
			}
			if got := string(w.reason()); got != tc.want {
				t.Fatalf("reason = %q, want %q", got, tc.want)
			}
		})
	}
}
