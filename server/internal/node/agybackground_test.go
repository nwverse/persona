package node

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func agyBackgroundLogURI(path string) string {
	return (&url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(path), "/")}).String()
}

func agyBackgroundStep(index int, status, started string, task int, description, logURI string) string {
	content := fmt.Sprintf("Created At: %s\n%s %s/task-%d\nTask Description: %s", started, agyTaskPrefix, agyTestID, task, description)
	if logURI != "" {
		content += "\nTask logs are available at: " + logURI
	}
	return fmt.Sprintf(`{"step_index":%d,"type":"GENERIC","status":%q,"created_at":%q,"content":%q}`+"\n", index, status, started, content)
}

func TestAgyBackgroundLifecycle(t *testing.T) {
	n := &Node{home: t.TempDir()}
	t.Setenv("ANTIGRAVITY_CLI_CONFIG_DIR", "")
	dir := n.agyDataRoot()
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	finish := start.Add(time.Minute)
	started, ended := start.Format(time.RFC3339), finish.Format(time.RFC3339)
	logPath := filepath.Join(dir, "brain", agyTestID, ".system_generated", "tasks", "task-1.log")
	logURI := agyBackgroundLogURI(logPath)
	planner := fmt.Sprintf(`{"step_index":0,"type":"PLANNER_RESPONSE","created_at":%q,"tool_calls":[{"name":"read_file"},{"name":"run_command","args":{"CommandLine":"\"echo ready\""}},{"name":"schedule"},{"name":"run_workflow"},{"name":"invoke_subagent"}]}`+"\n", started)
	// The command's result arrives after its sibling's result.
	file := agyTestFile(t, dir, agyTestID, planner+
		agyBackgroundStep(3, "RUNNING", started, 2, "Cron: */30 * * * * Check status", "")+
		agyBackgroundStep(2, "RUNNING", started, 1, "Wait for ready", logURI)+
		agyBackgroundStep(4, "RUNNING", started, 3, "Run checks", "file:///unrelated/task-3.log")+
		agyBackgroundStep(5, "RUNNING", started, 4, "Review changes", ""))
	row := proto.Session{Status: proto.StatusIdle}
	n.mergeAgyTranscript(&row, agyTestID)
	if len(row.Background) != 4 || len(row.BackgroundEnded()) != 0 || row.Loop != nil {
		t.Fatalf("unexpected background state: %+v / %+v", row.Background, row.BackgroundEnded())
	}
	for i, kind := range []string{"shell", "monitor", "workflow", "agent"} {
		task := row.Background[i]
		if task.ID != fmt.Sprintf("%s/task-%d", agyTestID, i+1) || task.Kind != kind || !task.StartedAt.Equal(start) {
			t.Fatalf("task %d: %+v", i, task)
		}
	}
	if row.Background[0].Command != "echo ready" || row.Background[0].OutputFile != logPath || row.Background[1].Description != "Cron: */30 * * * * Check status" || row.Background[2].OutputFile != "" {
		t.Fatalf("lost or misattributed task metadata: %+v", row.Background)
	}

	agyTestAppend(t, file, fmt.Sprintf(`{"step_index":6,"type":"SYSTEM_MESSAGE","created_at":%q,"content":%q}`+"\n", ended,
		fmt.Sprintf("<SYSTEM_MESSAGE>\n[Message] taskId=%[1]s/task-1 message=Task id \"%[1]s/task-1\" finished with result: Ready\n</SYSTEM_MESSAGE>", agyTestID)))
	agyTestAppend(t, file, fmt.Sprintf(`{"step_index":7,"type":"PLANNER_RESPONSE","created_at":%q,"tool_calls":[{"name":"manage_task","args":{"Action":"\"kill\"","TaskId":"\"%s/task-2\""}},{"name":"manage_task","args":{"Action":"status","TaskId":"%s/task-4"}}]}`+"\n", ended, agyTestID, agyTestID))
	agyTestAppend(t, file, fmt.Sprintf(`{"step_index":4,"type":"GENERIC","status":"ERROR","created_at":%q,"updated_at":%q,"content":"Workflow failed"}`+"\n", started, ended))
	n.mergeAgyTranscript(&row, agyTestID)
	if len(row.Background) != 1 || row.Background[0].Kind != "agent" {
		t.Fatalf("running tasks: %+v", row.Background)
	}
	outcomes := row.BackgroundEnded()
	if len(outcomes) != 3 {
		t.Fatalf("ended tasks: %+v", outcomes)
	}
	for i, status := range []string{"completed", "killed", "failed"} {
		if outcomes[i].Status != status || !outcomes[i].EndedAt.Equal(finish) {
			t.Fatalf("outcome %d: %+v", i, outcomes[i])
		}
	}
	if outcomes[0].OutputFile != logPath || outcomes[2].Description != "Run checks" {
		t.Fatalf("lost outcome metadata: %+v", outcomes)
	}

	for _, status := range []string{proto.StatusOffline, proto.StatusInterrupted} {
		row.Status = status
		n.mergeAgyTranscript(&row, agyTestID)
		if len(row.Background) != 0 || len(row.BackgroundEnded()) != 3 {
			t.Fatalf("stopped row %s: %+v / %+v", status, row.Background, row.BackgroundEnded())
		}
	}
	row.Status = proto.StatusIdle
	agyTestAppend(t, file, fmt.Sprintf(`{"step_index":8,"type":"SYSTEM_MESSAGE","created_at":%q,"content":%q}`+"\n", ended,
		fmt.Sprintf(`Task id "%s/task-4" was canceled.`, agyTestID)))
	n.mergeAgyTranscript(&row, agyTestID)
	if outcomes := row.BackgroundEnded(); len(row.Background) != 0 || len(outcomes) != 4 || outcomes[3].Status != "killed" {
		t.Fatalf("canceled subagent: %+v / %+v", row.Background, outcomes)
	}

	old := time.Now().UTC().Add(-2 * workflowKeep).Format(time.RFC3339)
	agyTestWrite(t, file, agyBackgroundStep(0, "RUNNING", old, 1, "Old task", "")+
		fmt.Sprintf(`{"step_index":1,"type":"SYSTEM_MESSAGE","created_at":%q,"content":%q}`+"\n", old,
			fmt.Sprintf(`Task id "%s/task-1" was canceled.`, agyTestID)))
	n.mergeAgyTranscript(&row, agyTestID)
	if len(row.Background) != 0 || len(row.BackgroundEnded()) != 0 {
		t.Fatalf("old outcome survived: %+v / %+v", row.Background, row.BackgroundEnded())
	}
}

func TestAgyBackgroundSessionListMerge(t *testing.T) {
	n, herdr, home := herdrNode(t)
	t.Setenv("ANTIGRAVITY_CLI_CONFIG_DIR", "")
	dir := n.agyDataRoot()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	logPath := filepath.Join(dir, "brain", agyTestID, ".system_generated", "tasks", "task-1.log")
	logURI := agyBackgroundLogURI(logPath)
	agyTestFile(t, dir, agyTestID, `{"step_index":0,"type":"PLANNER_RESPONSE","tool_calls":[{"name":"schedule"}]}`+"\n"+
		agyBackgroundStep(1, "RUNNING", stamp, 1, "Cron: */30 * * * * Check status", logURI))

	const sid = "agy-" + agyTestID
	n.runtimes = newRuntimeRegistry()
	h := n.runtimes.add(nil)
	h.ready, h.hello.Runtime = true, agyRuntimeID
	setRuntimeRow := func(status string, running bool) {
		t.Helper()
		// Decode the companion's flat wire fields before normalizing as runtime.sessions does.
		var row proto.Session
		raw := fmt.Sprintf(`{"session_id":%q,"cwd":%q,"model":"gemini-test","status":%q,"background_ended":[{"id":%q,"kind":"workflow","status":"completed","ended_at":%q,"output_file":"/unrelated.log"}]}`,
			sid, home, status, agyTestID+"/task-2", stamp)
		if err := json.Unmarshal([]byte(raw), &row); err != nil {
			t.Fatal(err)
		}
		if running {
			row.Background = []proto.BackgroundTask{{ID: agyTestID + "/task-1", Kind: "monitor", OutputFile: "/unrelated.log"}}
		}
		n.runtimes.setSessions(h, normalizeRuntimeSessions(h, []proto.Session{row}))
	}
	setRuntimeRow(proto.StatusIdle, true)
	rows := listed(t, n)
	acp := rows[sid]
	if len(acp.Background) != 1 || acp.Background[0].Kind != "monitor" || acp.Background[0].OutputFile != logPath ||
		len(acp.BackgroundEnded()) != 1 || acp.BackgroundEnded()[0].Kind != "workflow" || acp.BackgroundEnded()[0].OutputFile != "" {
		t.Fatalf("ACP fields after normalization: %+v / %+v", acp.Background, acp.BackgroundEnded())
	}

	setRuntimeRow(proto.StatusIdle, false) // The companion sees a transcript without an ACP process.
	pane := agyTestAgent(agyTestPane, home, agyTestID)
	herdr.reply("session.snapshot", snapshot(nil, nil, []any{pane}, nil))
	rows = listed(t, n)
	row := rows[agyTestRow]
	if _, duplicate := rows[sid]; duplicate || row.Model != "gemini-test" || len(row.Background) != 1 || row.Background[0].Kind != "monitor" || row.Loop != nil {
		t.Fatalf("live pane merge: pane=%+v, duplicate=%v", row, duplicate)
	}

	setRuntimeRow(proto.StatusOffline, true)
	pane["agent_status"] = proto.StatusOffline
	herdr.reply("session.snapshot", snapshot(nil, nil, []any{pane}, nil))
	rows = listed(t, n)
	if len(rows[sid].Background) != 0 || len(rows[agyTestRow].Background) != 0 {
		t.Fatalf("stopped sessions show running work: %+v / %+v", rows[sid].Background, rows[agyTestRow].Background)
	}
}

func TestAgyBackgroundTaskLogRejectsEscapingSymlink(t *testing.T) {
	dir := t.TempDir()
	path := agyTestFile(t, dir, agyTestID, "")
	logPath := filepath.Join(filepath.Dir(filepath.Dir(path)), "tasks", "task-1.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.log")
	agyTestWrite(t, outside, "unrelated file")
	if err := os.Symlink(outside, logPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	brain, err := agyBrain(dir, agyTestID)
	if err != nil {
		t.Fatal(err)
	}
	defer brain.Close()
	uri := agyBackgroundLogURI(logPath)
	if path := agyTaskLog(uri, dir, agyTestID, "task-1", brain); path != "" {
		t.Fatalf("accepted a symlink outside the conversation: %s", path)
	}
}
